package cli

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/session"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newHandoffCmd() *cobra.Command {
	var sessionID string
	var launch bool
	cmd := &cobra.Command{
		Use:   "handoff [provider] [from-account] [to-account]",
		Short: "Copy a session from one account to another so you can resume it there",
		Long: "Copy a session transcript from one account into another, so you can " +
			"continue the same conversation under a different account — e.g. when one " +
			"hits its usage limit. Only the transcript is copied; credentials are never " +
			"touched.\n\nRun with no arguments in a terminal for an interactive picker, " +
			"or give <provider> <from> <to> to hand off the most recent session directly.",
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if len(args) == 3 {
				return handoffDirect(cmd, path, args[0], args[1], args[2], sessionID, launch)
			}
			if isTerminal(os.Stdin) {
				return handoffPickSource(path)
			}
			return fmt.Errorf("usage: aiacc handoff <provider> <from-account> <to-account>")
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id to hand off (default: the most recent)")
	cmd.Flags().BoolVar(&launch, "launch", false, "launch Claude Code resuming the session immediately")
	return cmd
}

// handoffDirect is the non-interactive path: hand off one session (the most
// recent, or --session) and print the resume command.
func handoffDirect(cmd *cobra.Command, cfgPath, providerName, from, to, sessionID string, launch bool) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	fromDir, err := provider.AccountDir(c, providerName, from)
	if err != nil {
		return fmt.Errorf("from account: %w", err)
	}
	toDir, err := provider.AccountDir(c, providerName, to)
	if err != nil {
		return fmt.Errorf("to account: %w", err)
	}
	sess, err := pickSession(fromDir, sessionID)
	if err != nil {
		return err
	}
	meta, _ := session.ReadMeta(sess.Path)
	if _, err := session.Copy(sess, toDir); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Handed off session %s → %s\n", shortID(sess.ID), to)
	if meta.Title != "" {
		fmt.Fprintf(out, "  %q\n", truncate(meta.Title, 60))
	}
	fmt.Fprintln(out, "Resume it:")
	if meta.Cwd != "" {
		fmt.Fprintf(out, "  cd %s && %s --resume %s\n", meta.Cwd, to, sess.ID)
	} else {
		fmt.Fprintf(out, "  %s --resume %s\n", to, sess.ID)
	}

	if launch {
		if meta.Cwd == "" {
			return fmt.Errorf("can't launch: session has no recorded cwd — run the resume command yourself")
		}
		return launchResume(toDir, meta.Cwd, sess.ID)
	}
	return nil
}

// handoffPickSource starts the interactive flow by choosing the source account.
func handoffPickSource(cfgPath string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	accts := claudeAccounts(c)
	if len(accts) < 2 {
		return tui.RunMessage("hand off", []tui.Line{{Text: "Need at least two accounts to hand off between.", Color: tui.Grey}})
	}
	items := make([]tui.ListItem, len(accts))
	for i, a := range accts {
		items[i] = tui.ListItem{Primary: a.account}
	}
	i, err := tui.RunList("hand off — from which account?", items)
	if err != nil || i < 0 {
		return err
	}
	return runHandoffTUI(cfgPath, accts[i].provider, accts[i].account)
}

// runHandoffTUI runs the interactive flow with the source account fixed: pick a
// session, pick a target account, copy, and show the resume command. Used by the
// picker's `h` and by handoffPickSource.
func runHandoffTUI(cfgPath, sProvider, sAccount string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	sDir, err := provider.AccountDir(c, sProvider, sAccount)
	if err != nil {
		return err
	}
	sessions, err := session.List(sDir)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return tui.RunMessage("hand off · "+sAccount, []tui.Line{{Text: "No sessions found for this account yet.", Color: tui.Grey}})
	}
	if len(sessions) > 30 {
		sessions = sessions[:30] // most recent 30 is plenty to choose from
	}

	items := make([]tui.ListItem, len(sessions))
	metas := make([]session.Meta, len(sessions))
	for i, s := range sessions {
		m, _ := session.ReadMeta(s.Path)
		metas[i] = m
		title := m.Title
		if title == "" {
			title = "(untitled)"
		}
		sec := shortID(s.ID) + " · " + humanAge(s.Modified)
		if m.Cwd != "" {
			sec += " · " + filepath.Base(m.Cwd)
		}
		items[i] = tui.ListItem{Primary: title, Secondary: sec}
	}
	si, err := tui.RunList("hand off — which session? (from "+sAccount+")", items)
	if err != nil || si < 0 {
		return err
	}
	sess, meta := sessions[si], metas[si]

	var targets []acctRef
	for _, a := range claudeAccounts(c) {
		if !(a.provider == sProvider && a.account == sAccount) {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return tui.RunMessage("hand off", []tui.Line{{Text: "No other account to hand off to.", Color: tui.Grey}})
	}
	titems := make([]tui.ListItem, len(targets))
	for i, t := range targets {
		titems[i] = tui.ListItem{Primary: t.account}
	}
	ti, err := tui.RunList("hand off — to which account?", titems)
	if err != nil || ti < 0 {
		return err
	}
	target := targets[ti]

	if _, err := session.Copy(sess, target.dir); err != nil {
		return err
	}
	body := []tui.Line{
		{Text: "✓ Copied to " + target.account, Color: tui.Green},
		{},
		{Text: "Resume it:", Color: tui.White},
	}
	if meta.Cwd != "" {
		body = append(body, tui.Line{Text: "cd " + meta.Cwd, Color: tui.Blue})
	}
	body = append(body, tui.Line{Text: target.account + " --resume " + sess.ID, Color: tui.Blue})
	return tui.RunMessage("hand off · done", body)
}

// acctRef is a launchable account (provider, account, expanded dir).
type acctRef struct{ provider, account, dir string }

// claudeAccounts lists the launchable (claude) accounts, sorted.
func claudeAccounts(c *config.Config) []acctRef {
	var out []acctRef
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if launchCommand(c, pn) == "" {
			continue
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			out = append(out, acctRef{pn, an, dir})
		}
	}
	return out
}

// pickSession resolves the requested session id, or the most recent one.
func pickSession(fromDir, id string) (session.Info, error) {
	if id != "" {
		return session.Find(fromDir, id)
	}
	list, err := session.List(fromDir)
	if err != nil {
		return session.Info{}, err
	}
	if len(list) == 0 {
		return session.Info{}, fmt.Errorf("no sessions found in %s", fromDir)
	}
	return list[0], nil
}

// launchResume changes into the session's cwd and execs Claude Code resuming it
// under the target account. On success it never returns.
func launchResume(toDir, cwd, id string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH — is it installed?")
	}
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	env := setEnv(os.Environ(), "CLAUDE_CONFIG_DIR", toDir)
	return syscall.Exec(bin, []string{"claude", "--resume", id}, env)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// humanAge renders how long ago t was, compactly.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

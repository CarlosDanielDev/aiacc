package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/session"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

// execResume replaces this process with Claude Code resuming a session. A
// variable so tests can record the launch instead of exec'ing.
var execResume = launchResume

// pickList shows the account and session lists, the latter with the bypass
// toggle. A variable so tests can choose a row without a tty.
var pickList = tui.RunListToggle

func newResumeCmd() *cobra.Command {
	var sessionID, cwd string
	var printOnly, bypass bool
	cmd := &cobra.Command{
		Use:   "resume [provider] [account]",
		Short: "Resume a session in the same account, in its original directory",
		Long: "Continue an earlier Claude Code session in the account it already lives in: " +
			"aiacc changes into the session's recorded directory and runs claude with the " +
			"account's config dir and launch args plus --resume. Nothing is copied — to " +
			"continue under a different account, use `aiacc handoff`.\n\nRun with no " +
			"arguments in a terminal for an interactive picker, or give <provider> <account> " +
			"to resume the most recent session directly.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			if len(args) == 2 {
				return resumeDirect(cmd, path, args[0], args[1], sessionID, cwd, printOnly, bypass)
			}
			// The picker would drop these flags, and drop --print by launching.
			for _, f := range []string{"print", "session", "cwd", "bypass"} {
				if cmd.Flags().Changed(f) {
					return fmt.Errorf("--%s needs <provider> <account>: aiacc resume <provider> <account> --%s …", f, f)
				}
			}
			if len(args) == 0 && isTerminal(os.Stdin) {
				return resumePickAccount(path)
			}
			return fmt.Errorf("usage: aiacc resume <provider> <account> [--session <id>] [--cwd <dir>] [--print] [--bypass]")
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id, or a unique prefix of it as the picker shows (default: the most recent)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "resume from this directory instead of the session's recorded one (it must exist)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the command instead of running it")
	cmd.Flags().BoolVar(&bypass, "bypass", false, "resume with the provider's permission-bypass flag, so the agent runs tools without asking")
	return cmd
}

// resumeDirect resumes one session (the most recent, or --session) of the
// account in place, or prints the command with --print. With bypass it adds
// the provider's bypass flag after the launch args. It writes nothing.
func resumeDirect(cmd *cobra.Command, cfgPath, providerName, account, sessionID, cwdFlag string, printOnly, bypass bool) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	p, ok := c.Providers[providerName]
	if !ok {
		return fmt.Errorf("unknown provider %q — known: %s", providerName, strings.Join(slices.Sorted(maps.Keys(c.Providers)), ", "))
	}
	if _, ok := p.Accounts[account]; !ok {
		return fmt.Errorf("unknown account %q for %s — known: %s", account, providerName, strings.Join(slices.Sorted(maps.Keys(p.Accounts)), ", "))
	}
	if !launchesClaude(c, providerName) {
		return fmt.Errorf("resume is claude-only — it resumes Claude Code transcripts, and %q is not a claude profile (command 'claude', env 'CLAUDE_CONFIG_DIR')", providerName)
	}
	args := provider.LaunchArgs(c, providerName, account)
	if bypass {
		flag := provider.BypassFlag(c, providerName)
		if flag == "" {
			return fmt.Errorf("--bypass: provider %q has no known permission-bypass flag", providerName)
		}
		args = append(args, flag)
	}
	dir, err := provider.AccountDir(c, providerName, account)
	if err != nil {
		return err
	}
	sessions, err := session.List(dir)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions found in %s", dir)
	}
	sess := sessions[0]
	if sessionID != "" {
		if sess, err = findSession(dir, sessions, sessionID); err != nil {
			return err
		}
	}
	meta, err := readMeta(sess)
	if err != nil {
		return err
	}
	cwd, err := resumeCwd(meta.Cwd, cwdFlag)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.ErrOrStderr(), resumingLine(sess, meta, cwd, bypass))
	if printOnly {
		fmt.Fprintln(cmd.OutOrStdout(), printedResume(dir, args, cwd, sess.ID))
		return nil
	}
	return execResume(dir, args, cwd, sess.ID)
}

// findSession resolves an exact id or a unique prefix of one with
// session.Find, the matcher handoff uses. Only on a refusal does it build its
// own message: the matches of an ambiguous prefix, or the account's sessions
// for an unknown one, so the user can pick without another command.
func findSession(dir string, sessions []session.Info, id string) (session.Info, error) {
	s, err := session.Find(dir, id)
	if err == nil {
		return s, nil
	}
	// Find's own refusals (not found, ambiguous) are plain messages; a failure
	// to list the projects dir is a *fs.PathError, returned unchanged.
	if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
		return session.Info{}, err
	}
	var hits []session.Info
	for _, s := range sessions {
		if id != "" && strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	what := "is ambiguous"
	if len(hits) < 2 {
		what, hits = "matches no session", sessions
	}
	if len(hits) > 10 {
		hits = hits[:10]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session %q %s — candidates:", id, what)
	for _, s := range hits {
		m, _ := session.ReadMeta(s.Path)
		fmt.Fprintf(&b, "\n  %s  %s  %s", shortID(s.ID), humanAge(s.Modified), titleOf(m))
	}
	return session.Info{}, fmt.Errorf("%s", b.String())
}

// resumeCwd is the directory to resume in: --cwd when given, else the
// session's recorded cwd. Either must exist; claude would otherwise start in
// a directory the session's file paths do not belong to.
func resumeCwd(recorded, override string) (string, error) {
	if override != "" {
		if fi, err := os.Stat(override); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("--cwd %s is not an existing directory", override)
		}
		return override, nil
	}
	if recorded == "" {
		return "", fmt.Errorf("the session has no recorded directory — pass --cwd <dir> to resume it from one")
	}
	if fi, err := os.Stat(recorded); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the session's directory %s no longer exists — pass --cwd <dir> to resume it from another", recorded)
	}
	return recorded, nil
}

// readMeta reads the session's transcript for resuming. A read error is its
// own refusal: without it the empty Meta would read as "no recorded directory".
func readMeta(s session.Info) (session.Meta, error) {
	m, err := session.ReadMeta(s.Path)
	if err != nil {
		return m, fmt.Errorf("can't read session transcript %s: %w", s.Path, err)
	}
	return m, nil
}

func titleOf(m session.Meta) string {
	if m.Title == "" {
		return "(untitled)"
	}
	return truncate(m.Title, 60)
}

// resumingLine announces the resume, ending in "(bypass on)" when the launch
// carries the permission-bypass flag.
func resumingLine(s session.Info, m session.Meta, cwd string, bypass bool) string {
	line := fmt.Sprintf("Resuming %s %q (%s) in %s", shortID(s.ID), titleOf(m), humanAge(s.Modified), cwd)
	if bypass {
		line += " (bypass on)"
	}
	return line
}

// printedResume is what --print shows: the cd, the config-dir env var and the
// exact argv launchResume would exec, quoted for a POSIX shell.
func printedResume(dir string, args []string, cwd, id string) string {
	argv, _ := resumeCmd(args, dir, id, nil)
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	return "cd " + shellWord(cwd) + " && CLAUDE_CONFIG_DIR=" + shellWord(dir) + " " + strings.Join(words, " ")
}

// shellWord leaves a word a shell reads literally as it is and quotes any
// other. The cwd and session id come from the transcript, not from aiacc.
func shellWord(s string) string {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:+@%,", r))
	}) {
		return shell.PosixQuote(s)
	}
	return s
}

// resumePickAccount starts the bare interactive `aiacc resume` by choosing the
// account.
func resumePickAccount(cfgPath string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	accts := claudeAccounts(c)
	if len(accts) == 0 {
		return showMessage("resume", []tui.Line{{Text: "Resume is claude-only — it resumes Claude Code transcripts, and no claude accounts are configured.", Color: tui.Grey}})
	}
	items := make([]tui.ListItem, len(accts))
	for i, a := range accts {
		items[i] = tui.ListItem{Primary: a.account}
	}
	i, _, err := pickList("resume — which account?", items, "")
	if err != nil || i < 0 {
		return err
	}
	return runResumeTUI(cfgPath, accts[i].provider, accts[i].account)
}

// runResumeTUI lists the account's sessions and resumes the chosen one in
// place. Refusals are shown on screen and launch nothing. Used by the
// picker's `u` and by resumePickAccount.
func runResumeTUI(cfgPath, providerName, account string) error {
	c, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if !launchesClaude(c, providerName) {
		return showMessage("resume", []tui.Line{{Text: "Resume is claude-only — it resumes Claude Code transcripts.", Color: tui.Grey}})
	}
	dir, err := provider.AccountDir(c, providerName, account)
	if err != nil {
		return err
	}
	sessions, items, _, err := sessionChoices(dir)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return showMessage("resume · "+account, []tui.Line{{Text: "No sessions found in " + dir + ".", Color: tui.Grey}})
	}
	// The bypass toggle is offered only where the provider has a flag for it,
	// and starts off on every open: aiacc never adds the flag on its own.
	bypassFlag := provider.BypassFlag(c, providerName)
	toggle := ""
	if bypassFlag != "" {
		toggle = "bypass"
	}
	si, bypass, err := pickList("resume — which session? ("+account+")", items, toggle)
	if err != nil || si < 0 {
		return err
	}
	// The list tolerates an unreadable transcript; resuming one does not.
	meta, err := readMeta(sessions[si])
	if err != nil {
		return showMessage("resume · "+account, []tui.Line{{Text: err.Error(), Color: tui.Grey}})
	}
	cwd, err := resumeCwd(meta.Cwd, "")
	if err != nil {
		hint := "From a shell: aiacc resume " + providerName + " " + account + " --session " + shortID(sessions[si].ID) + " --cwd <dir>"
		if bypass {
			hint += " --bypass"
		}
		return showMessage("resume · "+account, []tui.Line{
			{Text: err.Error(), Color: tui.Grey},
			{},
			{Text: hint, Color: tui.Blue},
		})
	}
	fmt.Println(resumingLine(sessions[si], meta, cwd, bypass))
	args := provider.LaunchArgs(c, providerName, account)
	if bypass {
		args = append(args, bypassFlag)
	}
	return execResume(dir, args, cwd, sessions[si].ID)
}

// sessionChoices is the session list the hand-off and resume pickers show:
// the account's most recent 30 sessions, their list items and parsed meta.
func sessionChoices(dir string) ([]session.Info, []tui.ListItem, []session.Meta, error) {
	sessions, err := session.List(dir)
	if err != nil {
		return nil, nil, nil, err
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
	return sessions, items, metas, nil
}

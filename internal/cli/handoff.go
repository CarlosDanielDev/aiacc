package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/session"
	"github.com/spf13/cobra"
)

func newHandoffCmd() *cobra.Command {
	var sessionID string
	var launch bool
	cmd := &cobra.Command{
		Use:   "handoff <provider> <from-account> <to-account>",
		Short: "Copy a session from one account to another so you can resume it there",
		Long: "Copy a session transcript from one account into another, so you can " +
			"continue the same conversation under a different account — e.g. when one " +
			"hits its usage limit. Only the transcript is copied; credentials are never " +
			"touched. Defaults to the most recent session; use --session to pick one.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, from, to := args[0], args[1], args[2]
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
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
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id to hand off (default: the most recent)")
	cmd.Flags().BoolVar(&launch, "launch", false, "launch Claude Code resuming the session immediately")
	return cmd
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

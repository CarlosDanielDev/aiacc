// Package cli wires the aiacc command tree.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// NewRoot builds the aiacc command tree. A bare `aiacc` in an interactive
// terminal is the front door: the profile launcher. Run without a terminal
// (piped, redirected, CI) it prints help, so a scripted `aiacc` never blocks on
// a UI nothing can drive.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "aiacc",
		Short:         "Launch Claude Code (and other CLIs) under isolated account profiles",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Poka-yoke for the v0.7 upgrade: a leftover pre-v0.7 shell hook
			// wraps bare `aiacc` and calls us with `--shell <name>`. That flag no
			// longer exists, so without this the upgrade greets the user with a
			// cryptic "unknown flag: --shell". Absorb it (the flag is hidden and
			// ignored) and point them at the fix; empty stdout keeps the old
			// hook's `eval $(...)` a harmless no-op.
			if cmd.Flags().Changed("shell") {
				fmt.Fprintln(cmd.ErrOrStderr(), "aiacc: an old aiacc shell hook is still loaded in this shell. "+
					"Re-source the launchers to replace it:\n"+
					"  bash/zsh:  eval \"$(aiacc shell-init <shell>)\"\n"+
					"  fish:      aiacc shell-init fish | source\n"+
					"(or just open a new terminal).")
				return nil
			}
			if !isTerminal(os.Stdin) {
				return cmd.Help()
			}
			return runPicker("")
		},
	}
	// Hidden, ignored: absorbs `--shell` from a leftover pre-v0.7 hook so the
	// upgrade never errors. See the RunE note above.
	root.Flags().String("shell", "", "")
	_ = root.Flags().MarkHidden("shell")
	root.AddCommand(
		newAddCmd(),
		newRemoveCmd(),
		newRenameCmd(),
		newListCmd(),
		newStatusCmd(),
		newUsageCmd(),
		newSetupCmd(),
		newShellInitCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() error { return NewRoot().Execute() }

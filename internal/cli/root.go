// Package cli wires the aiacc command tree.
package cli

import (
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
			if !isTerminal(os.Stdin) {
				return cmd.Help()
			}
			return runPicker("")
		},
	}
	root.AddCommand(
		newAddCmd(),
		newRemoveCmd(),
		newListCmd(),
		newStatusCmd(),
		newUsageCmd(),
		newShellInitCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() error { return NewRoot().Execute() }

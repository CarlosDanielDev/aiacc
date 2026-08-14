// Package cli wires the aiacc command tree.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// NewRoot builds the aiacc command tree. A bare `aiacc` in an interactive
// terminal is the front door: it launches the same picker as `aiacc use`. Run
// without a terminal (piped, redirected, CI) it prints help instead, so a
// scripted `aiacc` never blocks on a UI nothing can drive.
func NewRoot() *cobra.Command {
	var shellName string
	root := &cobra.Command{
		Use:           "aiacc",
		Short:         "Switch and monitor multiple AI-CLI accounts",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTerminal(os.Stdin) {
				return cmd.Help()
			}
			return runPicker(cmd, shellName, "")
		},
	}
	// Mirrors `use --shell`: the shell hook passes it so the front door can emit
	// the right export dialect for a chosen switch.
	root.Flags().StringVar(&shellName, "shell", defaultShell(), "shell dialect for the export line")
	root.AddCommand(
		newAddCmd(),
		newRemoveCmd(),
		newListCmd(),
		newUseCmd(),
		newStatusCmd(),
		newUsageCmd(),
		newShellInitCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() error { return NewRoot().Execute() }

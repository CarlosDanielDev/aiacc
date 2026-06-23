// Package cli wires the aiacc command tree.
package cli

import "github.com/spf13/cobra"

// version is overridden at build time via -ldflags.
var version = "dev"

// NewRoot builds the aiacc command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "aiacc",
		Short:   "Switch and monitor multiple AI-CLI accounts",
		Version: version,
	}
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

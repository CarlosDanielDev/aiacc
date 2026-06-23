package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		fmt.Fprintf(c.OutOrStdout(), "%s: not implemented yet\n", name)
		return nil
	}
}

func newAddCmd() *cobra.Command {
	return &cobra.Command{Use: "add <provider> <account>", Short: "Register an account", RunE: notImplemented("add")}
}
func newRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove <provider> <account>", Short: "Unregister an account", RunE: notImplemented("remove")}
}
func newListCmd() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List providers and accounts", RunE: notImplemented("list")}
}
func newUseCmd() *cobra.Command {
	return &cobra.Command{Use: "use <provider> <account>", Short: "Switch the shell to an account", RunE: notImplemented("use")}
}
func newStatusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show active account per provider", RunE: notImplemented("status")}
}
func newUsageCmd() *cobra.Command {
	return &cobra.Command{Use: "usage [provider]", Short: "Show token usage per account", RunE: notImplemented("usage")}
}
func newShellInitCmd() *cobra.Command {
	return &cobra.Command{Use: "shell-init <bash|zsh|fish>", Short: "Print the shell hook", RunE: notImplemented("shell-init")}
}

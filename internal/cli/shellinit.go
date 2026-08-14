package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/spf13/cobra"
)

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init <bash|zsh|fish>",
		Short: "Print the shell hook and per-account shortcut commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sh := args[0]
			hook, err := shell.Hook(sh)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprint(out, hook)
			fmt.Fprint(out, accountAliases(sh))
			return nil
		},
	}
}

// accountAliases builds the per-account shortcut functions (claude-work,
// claude-personal, …) for the current config. Best-effort: a missing or broken
// config just yields no aliases, never a failed shell-init, since this text is
// eval'd on every new shell.
func accountAliases(shellName string) string {
	path, err := configPath()
	if err != nil {
		return ""
	}
	c, err := config.Load(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			if alias, err := shell.AccountAlias(shellName, pn, an); err == nil && alias != "" {
				b.WriteString(alias)
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n# aiacc per-account shortcuts\n" + b.String()
}

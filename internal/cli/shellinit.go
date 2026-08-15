package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/spf13/cobra"
)

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init <bash|zsh|fish>",
		Short: "Print per-profile launcher commands to add to your startup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := launchers(args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

// launchers builds the per-profile launcher functions for the current config —
// one command per profile, named after the account (e.g. `claude-work`), that
// runs the provider's CLI with that profile's directory. A missing/broken config
// yields the header with no functions rather than an error.
func launchers(shellName string) (string, error) {
	// Validate the shell up front so an unknown one is reported even with no
	// profiles yet.
	if _, err := shell.Launcher(shellName, "probe", "claude", "CLAUDE_CONFIG_DIR", "/x"); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# aiacc profile launchers\n")

	path, err := configPath()
	if err != nil {
		return b.String(), nil
	}
	c, err := config.Load(path)
	if err != nil {
		return b.String(), nil
	}
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		env, _ := provider.EnvVar(c, pn)
		cmd := launchCommand(pn)
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			fn, err := shell.Launcher(shellName, an, cmd, env, dir)
			if err == nil && fn != "" {
				b.WriteString(fn)
			}
		}
	}
	return b.String(), nil
}

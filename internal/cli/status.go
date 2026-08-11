package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show active account per provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tACCOUNT\tACTIVE\tLAST-USED")
			for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
				p := c.Providers[pn]
				env, _ := provider.EnvVar(c, pn)
				for _, a := range slices.Sorted(maps.Keys(p.Accounts)) {
					dir, err := provider.AccountDir(c, pn, a)
					if err != nil {
						continue
					}
					active := ""
					if env != "" && os.Getenv(env) == dir {
						active = "*"
					}
					lastUsed := "-"
					if fi, err := os.Stat(dir); err == nil {
						lastUsed = fi.ModTime().Format("2006-01-02 15:04")
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", pn, a, active, lastUsed)
				}
			}
			return w.Flush()
		},
	}
}

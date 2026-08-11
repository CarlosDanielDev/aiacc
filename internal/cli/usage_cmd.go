package cli

import (
	"fmt"
	"maps"
	"slices"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/usage"
	"github.com/spf13/cobra"
)

func newUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage [provider]",
		Short: "Show token usage per account",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tACCOUNT\tINPUT\tOUTPUT\tTOTAL\tUSED/QUOTA")
			for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
				if filter != "" && pn != filter {
					continue
				}
				p := c.Providers[pn]
				for _, a := range slices.Sorted(maps.Keys(p.Accounts)) {
					dir, err := provider.AccountDir(c, pn, a)
					if err != nil {
						continue
					}
					t, _ := usage.Aggregate(dir)
					acct := p.Accounts[a]
					usedQuota := "-"
					if acct.Quota > 0 {
						usedQuota = fmt.Sprintf("%d/%d", t.Total(), acct.Quota)
					}
					fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\n", pn, a, t.Input, t.Output, t.Total(), usedQuota)
				}
			}
			return w.Flush()
		},
	}
}

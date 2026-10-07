package cli

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/share"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newLinkCmd() *cobra.Command {
	var replace, prune bool
	cmd := &cobra.Command{
		Use:   "link [provider] [account]",
		Short: "Share your skills, sub-agents, commands and hooks with every profile",
		Long: "Symlink the authored assets of a CLI's own config directory — skills, " +
			"sub-agents, slash commands, hooks, instructions, settings — into each " +
			"account directory, so a profile launcher like `claude-work` has exactly " +
			"the tooling the bare `claude` has.\n\n" +
			"Credentials, transcripts and usage stay per-account. An entry a profile " +
			"already has of its own is left untouched; --replace takes it over, " +
			"renaming the original to <name>.aiacc-bak first.\n\n" +
			"--prune removes shared symlinks whose target no longer exists — the dead " +
			"links left behind when the CLI renames or drops a config directory. It " +
			"lists them and asks before removing anything.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			if prune {
				return runPrune(cmd, c, args)
			}
			results, err := linkAccounts(c, args, replace)
			if err != nil {
				return err
			}
			if isTerminal(os.Stdout) {
				return tui.RunMessage("aiacc — shared assets", linkLines(results, replace))
			}
			printLinkReport(cmd.OutOrStdout(), results, replace)
			return nil
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "take over entries a profile already has (originals renamed to <name>.aiacc-bak)")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove shared symlinks whose target no longer exists (asks first)")
	return cmd
}

// runPrune lists the dead shared links, asks, and only then removes them.
//
// Poka-yoke: removing a symlink is not aiacc's to do silently — the user may
// have made it by hand. So the dry pass runs first, its result is what the
// question is about, and a bare Enter declines. Nothing to prune exits without
// asking anything. Without a terminal the explicit --prune flag is taken as the
// answer: a script that passed it has already decided, and blocking on a prompt
// nothing can answer would hang CI.
func runPrune(cmd *cobra.Command, c *config.Config, args []string) error {
	dead := danglingLinks(c, args)
	w := cmd.OutOrStdout()
	if len(dead) == 0 {
		fmt.Fprintln(w, "No dead shared links.")
		return nil
	}
	var n int
	for _, d := range dead {
		n += len(d.names)
	}
	fmt.Fprintf(w, "%d dead shared link(s) — the target no longer exists:\n", n)
	for _, d := range dead {
		for _, name := range d.names {
			fmt.Fprintf(w, "  %-16s %s\n", d.account, name)
		}
	}
	if isTerminal(os.Stdin) && !confirm(bufio.NewReader(cmd.InOrStdin()), w, "Remove them?") {
		fmt.Fprintln(w, "Left alone.")
		return nil
	}
	var removed, failed int
	for _, d := range dead {
		res := share.Prune(d.dir, d.names)
		removed += res.Count(share.Pruned)
		failed += res.Count(share.Failed)
	}
	fmt.Fprintf(w, "Removed %d link(s).\n", removed)
	if failed > 0 {
		fmt.Fprintf(w, "%d could not be removed — check permissions on the profile dir.\n", failed)
	}
	return nil
}

// deadLinks is one account's dangling shared entries.
type deadLinks struct {
	account, dir string
	names        []string
}

// danglingLinks is the read-only pass: which shared links are dead, per account.
func danglingLinks(c *config.Config, args []string) []deadLinks {
	var out []deadLinks
	for _, p := range sharedProfiles(c, args) {
		var names []string
		for _, e := range share.Check(p.base, p.dir, p.entries).Entries {
			if e.Status == share.Dangling {
				names = append(names, e.Name)
			}
		}
		if len(names) > 0 {
			out = append(out, deadLinks{p.account, p.dir, names})
		}
	}
	return out
}

// confirm asks a yes/no question, defaulting to no.
func confirm(r *bufio.Reader, w io.Writer, question string) bool {
	s := strings.ToLower(strings.TrimSpace(ask(r, w, question+" [y/N] ")))
	return s == "y" || s == "yes"
}

// sharedProfile is one account that receives a provider's shared assets.
type sharedProfile struct {
	account, base, dir string
	entries            []string
}

// sharedProfiles selects the accounts named by args — none = every account, one
// = every account of that provider, two = that one account — skipping custom
// providers, whose asset layout aiacc cannot know.
func sharedProfiles(c *config.Config, args []string) []sharedProfile {
	var out []sharedProfile
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		if len(args) >= 1 && args[0] != pn {
			continue
		}
		base, entries, ok := provider.Shared(pn)
		if !ok {
			continue
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			if len(args) == 2 && args[1] != an {
				continue
			}
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			out = append(out, sharedProfile{an, base, dir, entries})
		}
	}
	return out
}

// linkAccounts links the accounts named by args (see sharedProfiles).
func linkAccounts(c *config.Config, args []string, replace bool) ([]share.Result, error) {
	var out []share.Result
	for _, p := range sharedProfiles(c, args) {
		res, err := share.Link(p.base, p.dir, p.entries, replace)
		if err != nil {
			return out, err
		}
		res.Dir = p.account // label the row by account, not path
		out = append(out, res)
	}
	return out, nil
}

// linkProfile shares the assets into one freshly-added account. Best-effort: a
// failure here never fails the add — the profile still launches, just bare.
func linkProfile(providerName, dir string) {
	base, entries, ok := provider.Shared(providerName)
	if !ok {
		return
	}
	_, _ = share.Link(base, dir, entries, false)
}

// sharedSummary links every account (never replacing) and reports how many
// profiles ended up with assets and how many entries were left alone because the
// profile owns them. Used by `setup`, so the one-step install also makes every
// profile equal to the bare CLI.
func sharedSummary(c *config.Config) (profiles, conflicts int) {
	results, err := linkAccounts(c, nil, false)
	if err != nil {
		return 0, 0
	}
	for _, r := range results {
		if r.Count(share.Linked, share.Already) > 0 {
			profiles++
		}
		conflicts += len(r.Conflicts())
	}
	return profiles, conflicts
}

func printLinkReport(w io.Writer, results []share.Result, replace bool) {
	if len(results) == 0 {
		fmt.Fprintln(w, "No profiles to share assets with.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ACCOUNT\tSHARED\tLEFT ALONE")
	var conflicts int
	for _, r := range results {
		left := r.Conflicts()
		conflicts += len(left)
		names := make([]string, len(left))
		for i, e := range left {
			names[i] = e.Name
		}
		detail := "-"
		if len(names) > 0 {
			detail = strings.Join(names, ", ")
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Dir, r.Count(share.Linked, share.Already), detail)
	}
	tw.Flush()
	if conflicts > 0 && !replace {
		fmt.Fprintln(w, "\nRun `aiacc link --replace` to share those too (originals kept as <name>.aiacc-bak).")
	}
}

// linkLines is the framed report shown on a terminal.
func linkLines(results []share.Result, replace bool) []tui.Line {
	if len(results) == 0 {
		return []tui.Line{{Text: "No profiles to share assets with.", Color: tui.Grey}}
	}
	body := []tui.Line{{Text: "skills · sub-agents · commands · hooks", Color: tui.Grey}, {Text: ""}}
	var conflicts int
	for _, r := range results {
		conflicts += len(r.Conflicts())
		body = append(body, tui.Line{
			Text:  fmt.Sprintf("✓ %-16s %d shared", r.Dir, r.Count(share.Linked, share.Already)),
			Color: tui.White,
		})
	}
	if conflicts > 0 && !replace {
		body = append(body,
			tui.Line{Text: ""},
			tui.Line{Text: fmt.Sprintf("%d entry(s) left alone — a profile owns them.", conflicts), Color: tui.Grey},
			tui.Line{Text: "aiacc link --replace  takes them over", Color: tui.Blue},
		)
	}
	return body
}

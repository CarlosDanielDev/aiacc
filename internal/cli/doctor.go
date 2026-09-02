package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/share"
	"github.com/CarlosDanielDev/aiacc/internal/shell"
	"github.com/spf13/cobra"
)

// errUnhealthy makes `aiacc doctor` exit non-zero when something is actually
// broken, so it can gate a shell startup file or a CI step. The report is on
// stdout already; this only carries the verdict.
var errUnhealthy = errors.New("doctor found problems (see above)")

// severity orders findings from fine to broken. Only sevFail sets the exit code:
// a warning is drift worth seeing, not a reason to fail someone's shell startup.
type severity int

const (
	sevOK severity = iota
	sevWarn
	sevFail
)

func (s severity) mark() string {
	switch s {
	case sevFail:
		return "✗"
	case sevWarn:
		return "!"
	default:
		return "✓"
	}
}

// finding is one diagnosis: a headline, plus the lines that explain or fix it.
type finding struct {
	sev    severity
	text   string
	detail []string
}

// section groups findings under a heading.
type section struct {
	title    string
	findings []finding
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that your launchers still work (read-only)",
		Long: "Answer one question: will `claude-work` still run the right Claude, with " +
			"the right config?\n\n" +
			"Launchers resolve their command by name at run time, which is what lets a " +
			"CLI self-update without breaking them — and what makes them go quietly " +
			"wrong when a second install appears on PATH, when the command becomes a " +
			"shell alias a /bin/sh script cannot see, or when a shared symlink outlives " +
			"the directory it pointed at. doctor makes that drift visible.\n\n" +
			"It changes nothing. It exits non-zero when something is broken, so it can " +
			"go in a shell startup file or CI.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			secs := diagnose(c)
			printReport(cmd.OutOrStdout(), secs)
			if worst(secs) == sevFail {
				return errUnhealthy
			}
			return nil
		},
	}
}

// diagnose runs every check, in the order the failures cascade: a command that
// cannot be resolved makes the launcher pointing at it moot, and a launcher that
// never runs makes its shared assets moot.
func diagnose(c *config.Config) []section {
	return []section{
		{"Commands", checkCommands(c)},
		{"Launchers", checkLaunchers(c)},
		{"Shared assets", checkShared(c)},
		{"Config surfaces", checkBase(c)},
	}
}

// ---- commands -------------------------------------------------------------

// checkCommands resolves each provider's command exactly the way its launcher
// will, then reports every *other* candidate on PATH. One install is fine; two
// means PATH order alone decides which version runs, and PATH order changes
// without anyone editing aiacc.
func checkCommands(c *config.Config) []finding {
	var out []finding
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		cmd := launchCommand(c, pn)
		if cmd == "" {
			out = append(out, finding{sevWarn,
				fmt.Sprintf("%s: no launch command configured", pn),
				[]string{"Profiles for this provider cannot be launched until one is set."}})
			continue
		}
		matches := lookAll(cmd)
		switch len(matches) {
		case 0:
			out = append(out, unresolvedCommand(pn, cmd))
		case 1:
			out = append(out, finding{sevOK,
				fmt.Sprintf("%s: %s → %s", pn, cmd, tildeize(matches[0])),
				[]string{cmdVersion(matches[0])}})
		default:
			detail := []string{fmt.Sprintf("launchers run %s (%s)", tildeize(matches[0]), cmdVersion(matches[0]))}
			for _, m := range matches[1:] {
				detail = append(detail, fmt.Sprintf("also on PATH: %s (%s)", tildeize(m), cmdVersion(m)))
			}
			detail = append(detail,
				"PATH order alone decides which one runs, so an update to either can",
				"invert it silently. Put the one you want earlier on PATH, or remove",
				"the other install.")
			out = append(out, finding{sevWarn,
				fmt.Sprintf("%s: %d installs of %s on PATH", pn, len(matches), cmd), detail})
		}
	}
	return out
}

// unresolvedCommand explains a command PATH cannot reach. The interesting case
// is not "it isn't installed" but "you can run it and your launcher can't":
// `claude migrate-installer` leaves a shell *alias*, and a /bin/sh launcher has
// no aliases, so the launcher fails on a CLI that works fine when typed.
func unresolvedCommand(pn, cmd string) finding {
	head := fmt.Sprintf("%s: %s not found on PATH", pn, cmd)
	found := probeInstalls(cmd)
	if len(found) == 0 {
		return finding{sevFail, head, []string{
			"Install it, or point aiacc at the command you do have:",
			"  aiacc add --command <cli>",
		}}
	}
	detail := []string{
		"Launchers are /bin/sh scripts: they resolve the command on PATH and cannot",
		"see a shell alias or function. An install is here, but PATH does not reach it:",
	}
	for _, p := range found {
		detail = append(detail, "  "+tildeize(p))
	}
	return finding{sevFail, head, append(detail,
		"Add that directory to PATH, so your shell and your launchers agree on which",
		"binary they mean.")}
}

// lookAll returns every executable named cmd on PATH, in PATH order — the
// `which -a` view that exec.LookPath collapses to its first hit. Element 0 is
// therefore exactly what a launcher gets.
func lookAll(cmd string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "." // POSIX: an empty PATH entry means the current directory
		}
		p := filepath.Join(dir, cmd)
		if seen[p] || !executable(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// executable reports whether p resolves to a runnable regular file. Stat follows
// symlinks, which is what makes a dangling link on PATH invisible here — the
// same way it is invisible to exec.LookPath.
func executable(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// probeInstalls looks for cmd where installers put it but PATH may not reach:
// the `migrate-installer` location, the usual user bins, the Homebrew prefixes,
// and npm's global bin. Only called once PATH has already failed.
func probeInstalls(cmd string) []string {
	home, _ := os.UserHomeDir()
	cands := []string{
		filepath.Join(home, "."+cmd, "local", cmd), // `claude migrate-installer`
		filepath.Join(home, ".local", "bin", cmd),
		filepath.Join(home, "bin", cmd),
		"/opt/homebrew/bin/" + cmd,
		"/usr/local/bin/" + cmd,
	}
	if bin := npmGlobalBin(); bin != "" {
		cands = append(cands, filepath.Join(bin, cmd))
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range cands {
		if seen[p] || !executable(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// npmGlobalBin asks npm where it installs global binaries. Node startup is slow,
// so this only runs on the path that is already broken.
func npmGlobalBin() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "bin")
}

// cmdVersion is the first line of `<bin> --version`. Diagnostics only: a CLI
// with no --version, or one that hangs, yields "unknown" rather than a stall.
func cmdVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "version unknown"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if line == "" {
		return "version unknown"
	}
	return line
}

// ---- launchers ------------------------------------------------------------

// checkLaunchers asks the user's own question — "if I type claude-work, what
// runs?" — by resolving each account name through PATH the way their shell will,
// rather than by looking only where aiacc would install.
func checkLaunchers(c *config.Config) []finding {
	var out []finding
	registered := map[string]bool{}
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		cmd := launchCommand(c, pn)
		env, _ := provider.EnvVar(c, pn)
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			registered[an] = true
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil || cmd == "" || env == "" {
				continue // already reported by checkCommands
			}
			out = append(out, checkLauncher(an, cmd, env, dir))
		}
	}
	return append(out, orphanLaunchers(registered)...)
}

// checkLauncher compares the script actually on PATH against the one setup would
// write today. A launcher that is merely *present* is not enough: the failure
// this exists to catch is a launcher that still runs and points somewhere stale.
func checkLauncher(account, cmd, env, dir string) finding {
	want, err := shell.LauncherScript(cmd, env, dir)
	if err != nil {
		return finding{sevFail,
			fmt.Sprintf("%s: cannot build a launcher", account),
			[]string{err.Error()}}
	}
	path, err := exec.LookPath(account)
	if err != nil {
		return finding{sevFail,
			fmt.Sprintf("%s: no launcher on PATH", account),
			[]string{"aiacc setup  installs it"}}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return finding{sevFail,
			fmt.Sprintf("%s: launcher unreadable", account),
			[]string{err.Error()}}
	}
	switch {
	case !strings.Contains(string(body), shell.LauncherMark):
		return finding{sevWarn,
			fmt.Sprintf("%s: %s is not an aiacc launcher", account, tildeize(path)),
			[]string{"Something else on PATH owns this name and shadows the profile."}}
	case string(body) != want:
		return finding{sevFail,
			fmt.Sprintf("%s: launcher is stale", account),
			[]string{
				fmt.Sprintf("It no longer matches the config: should run %s with %s=%s.", cmd, env, tildeize(dir)),
				"aiacc setup  rewrites it",
			}}
	}
	return finding{sevOK, fmt.Sprintf("%s → %s %s=%s", account, cmd, env, tildeize(dir)), nil}
}

// orphanLaunchers finds scripts aiacc wrote for accounts the config no longer
// has — left behind when an account is renamed or removed outside aiacc. The
// marker check keeps this from ever naming a file aiacc did not write.
func orphanLaunchers(registered map[string]bool) []finding {
	binDir, _ := launcherBinDir()
	ents, err := os.ReadDir(binDir)
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range ents {
		if e.IsDir() || registered[e.Name()] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(binDir, e.Name()))
		if err == nil && strings.Contains(string(b), shell.LauncherMark) {
			stale = append(stale, e.Name())
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return []finding{{sevWarn,
		fmt.Sprintf("%d launcher(s) for accounts aiacc no longer has", len(stale)),
		append(bullets(stale),
			"They still launch a profile nothing manages. Delete them from "+tildeize(binDir)+".")}}
}

// ---- shared assets --------------------------------------------------------

// checkShared inspects every profile's shared symlinks without touching them.
func checkShared(c *config.Config) []finding {
	var out []finding
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		base, entries, ok := provider.Shared(pn)
		if !ok {
			continue // custom provider — aiacc cannot know its asset layout
		}
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			dir, err := provider.AccountDir(c, pn, an)
			if err != nil {
				continue
			}
			res := share.Check(base, dir, entries)
			if len(res.Entries) == 0 {
				continue // this profile *is* the base dir; it owns the originals
			}
			out = append(out, sharedFindings(an, res)...)
		}
	}
	return out
}

func sharedFindings(account string, res share.Result) []finding {
	var dead, missing []string
	for _, e := range res.Entries {
		switch e.Status {
		case share.Dangling:
			dead = append(dead, e.Name)
		case share.Missing:
			missing = append(missing, e.Name)
		}
	}
	var out []finding
	if len(dead) > 0 {
		out = append(out, finding{sevFail,
			fmt.Sprintf("%s: %d dead shared link(s)", account, len(dead)),
			append(bullets(dead),
				"They point into "+tildeize(res.Base)+" at names that no longer exist —",
				"the CLI is handed a broken path as if it were an asset.",
				"aiacc link --prune  removes them (it asks first)")})
	}
	if len(missing) > 0 {
		out = append(out, finding{sevWarn,
			fmt.Sprintf("%s: %d shareable asset(s) not linked", account, len(missing)),
			append(bullets(missing), "aiacc link  shares them")})
	}
	if len(out) == 0 {
		out = append(out, finding{sevOK,
			fmt.Sprintf("%s: %d shared asset(s) live", account, res.Count(share.Already)), nil})
	}
	return out
}

// ---- config surfaces ------------------------------------------------------

// checkBase lists directories in a CLI's own config dir that aiacc neither
// shares nor recognises as per-account state. aiacc's shared list is hardcoded,
// so a surface the CLI adds later is missing from every profile in silence; this
// is what turns that silence into a question.
func checkBase(c *config.Config) []finding {
	var out []finding
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		extra, ok := provider.Unclassified(pn)
		if !ok || len(extra) == 0 {
			continue
		}
		base, _, _ := provider.Shared(pn)
		out = append(out, finding{sevWarn,
			fmt.Sprintf("%s: %d unclassified directory(s) in %s", pn, len(extra), tildeize(base)),
			append(bullets(extra),
				"aiacc neither shares these nor knows them as per-account state.",
				"If one holds authored assets, profiles are missing it — please open an",
				"issue so it joins the shared list.")})
	}
	return out
}

// ---- output ---------------------------------------------------------------

// printReport writes the plain-text report. Deliberately not the framed TUI the
// other commands use: doctor is meant for a shell startup file and for CI, and a
// screen that waits for a keypress would hang every new terminal.
func printReport(w io.Writer, secs []section) {
	for i, s := range secs {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, s.title)
		if len(s.findings) == 0 {
			fmt.Fprintln(w, "  · nothing to check")
			continue
		}
		for _, f := range s.findings {
			fmt.Fprintf(w, "  %s %s\n", f.sev.mark(), f.text)
			for _, d := range f.detail {
				fmt.Fprintf(w, "      %s\n", d)
			}
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, summary(secs))
}

func summary(secs []section) string {
	var fails, warns int
	for _, s := range secs {
		for _, f := range s.findings {
			switch f.sev {
			case sevFail:
				fails++
			case sevWarn:
				warns++
			}
		}
	}
	switch {
	case fails > 0:
		return fmt.Sprintf("✗ %d broken, %d to look at.", fails, warns)
	case warns > 0:
		return fmt.Sprintf("! %d to look at — nothing broken.", warns)
	default:
		return "✓ All good."
	}
}

func worst(secs []section) severity {
	out := sevOK
	for _, s := range secs {
		for _, f := range s.findings {
			if f.sev > out {
				out = f.sev
			}
		}
	}
	return out
}

// bullets indents names as detail lines.
func bullets(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "  " + n
	}
	return out
}

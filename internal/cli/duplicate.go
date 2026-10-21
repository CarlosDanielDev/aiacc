package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/share"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
	"github.com/spf13/cobra"
)

func newDuplicateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "duplicate <provider> <src> <new>",
		Short: "Register a new account shaped like src (quota, settings file) but with no login",
		Long: "Register <new> under <provider> with <src>'s quota and a private copy of its " +
			"settings file. The login, the sessions and the history are never copied: the new " +
			"profile logs in on its first run. The dir defaults to ~/.<provider>-<new>, " +
			"without doubling a <provider>- prefix <new> already has (claude-glm2 → ~/.claude-glm2).",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			d, err := duplicateAccount(path, args[0], args[1], args[2], dir)
			if err != nil {
				return err
			}
			for _, l := range d.lines() {
				w := cmd.OutOrStdout()
				if l.Color == tui.Red {
					w = cmd.ErrOrStderr()
				}
				fmt.Fprintln(w, l.Text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for the new account (default ~/.<provider>-<new>)")
	return cmd
}

// duplicated is what duplicateAccount made, for the CLI and the picker to report.
type duplicated struct {
	provider, src, name, dir string
	settings                 string // the copied settings file's registered path, "" when src has none
	launcher                 error  // a failed launcher sync; the duplicate itself stands
}

func (d duplicated) lines() []tui.Line {
	out := []tui.Line{{Text: fmt.Sprintf("Duplicated %s → %s (%s)", d.src, d.name, d.dir), Color: tui.Green}}
	if d.settings != "" {
		out = append(out, tui.Line{Text: "  copied: settings file " + d.settings, Color: tui.White})
	}
	out = append(out, tui.Line{Text: "  not copied: login, sessions, history", Color: tui.White})
	login := "log in with /login on first run"
	if d.provider != "claude" {
		login = "log in when prompted on first run"
	}
	out = append(out, tui.Line{Text: login, Color: tui.Blue})
	if d.launcher != nil {
		out = append(out, tui.Line{Text: "aiacc: " + d.launcher.Error(), Color: tui.Red})
	}
	return out
}

// duplicateAccount registers name under providerName shaped like src: src's
// quota, and a byte copy of src's settings file (mode 0600, never overwriting,
// never parsed — ADR-0007) in the new dir, which defaults to
// ~/.<provider>-<name>. Nothing else is copied: the login, the transcripts and
// every State entry belong to src, and the clone logs in on its first run.
// An existing dir is taken only when it is empty or holds nothing but the
// shared links linkProfile makes (what `aiacc remove` leaves behind).
//
// Every refusal is decided before the first write. A failure after this call
// created the dir removes what it created (the copy, then the dir) and nothing
// that was there before. The CLI and the picker both duplicate through here.
func duplicateAccount(cfgPath, providerName, src, name, dir string) (duplicated, error) {
	c, err := config.Load(cfgPath)
	if err != nil {
		return duplicated{}, err
	}
	acct, ok := c.Providers[providerName].Accounts[src]
	if !ok {
		what := fmt.Sprintf("unknown account %q under %s", src, providerName)
		if _, known := c.Providers[providerName]; !known {
			what = fmt.Sprintf("unknown provider %q", providerName)
		}
		return duplicated{}, fmt.Errorf("%s: known accounts are %s", what, knownAccounts(c))
	}
	if name == src {
		return duplicated{}, fmt.Errorf("%s already is %s: pick a new name for the duplicate", name, src)
	}
	if err := checkNewName(c, providerName, name); err != nil {
		return duplicated{}, err
	}
	if dir == "" {
		dir = provider.DefaultDir(providerName, name)
	}
	realDir, err := provider.ExpandHome(dir)
	if err != nil {
		return duplicated{}, err
	}
	srcDir, err := provider.AccountDir(c, providerName, src)
	if err != nil {
		return duplicated{}, err
	}
	if filepath.Clean(realDir) == filepath.Clean(srcDir) {
		return duplicated{}, fmt.Errorf("%s is %s's own dir: pick another dir (--dir)", dir, src)
	}
	ents, err := os.ReadDir(realDir)
	existed := err == nil
	switch {
	case existed && !onlySharedLinks(providerName, realDir, ents):
		return duplicated{}, fmt.Errorf("%s is not empty: pick another dir (--dir)", dir)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return duplicated{}, fmt.Errorf("%s cannot be used as the duplicate's dir: %w", dir, err)
	}
	if err := settingsFlagRefusal(providerName, acct.Settings); err != nil {
		return duplicated{}, err
	}
	// The settings bytes are read now, so a missing or unreadable file refuses
	// the duplicate before anything is written: never half a profile.
	var settings string
	var body []byte
	if acct.Settings != "" {
		srcSettings, err := provider.ExpandHome(acct.Settings)
		if err == nil {
			body, err = os.ReadFile(srcSettings)
		}
		if errors.Is(err, os.ErrNotExist) {
			return duplicated{}, fmt.Errorf("%s's settings file %s is missing: restore it, or change %s's settings first, then duplicate", src, acct.Settings, src)
		}
		if err != nil {
			return duplicated{}, fmt.Errorf("%s's settings file %s cannot be read: %w", src, acct.Settings, err)
		}
		settings = filepath.Join(dir, filepath.Base(acct.Settings))
		// A leftover dir holds shared links; the copy never writes through one.
		if _, err := os.Lstat(filepath.Join(realDir, filepath.Base(acct.Settings))); err == nil {
			return duplicated{}, fmt.Errorf("%s already has a %s: pick another dir (--dir)", dir, filepath.Base(acct.Settings))
		}
	}

	// MkdirAll makes every missing parent too, so record the whole chain it
	// is about to create (leaf first) — the rollback removes exactly those.
	var created []string
	if !existed {
		created = missingDirs(realDir)
		if err := os.MkdirAll(realDir, 0o755); err != nil {
			return duplicated{}, err
		}
	}
	var copied string
	undo := func() {
		if copied != "" {
			_ = os.Remove(copied)
		}
		for _, d := range created {
			_ = os.Remove(d) // only dirs this call made, and only if empty
		}
	}
	if body != nil {
		dst := filepath.Join(realDir, filepath.Base(acct.Settings))
		if err := writeNew(dst, body); err != nil {
			undo()
			return duplicated{}, err
		}
		copied = dst
	}
	if err := saveAccount(cfgPath, providerName, name, dir, acct.Quota, "", "", settings); err != nil {
		undo()
		return duplicated{}, err
	}
	linkProfile(providerName, realDir) // same skills/agents as the bare CLI
	return duplicated{
		provider: providerName, src: src, name: name, dir: dir, settings: settings,
		launcher: syncLauncher(cfgPath, providerName, name),
	}, nil
}

// onlySharedLinks reports whether every entry of dir is a link linkProfile
// makes: a shared name pointing at the provider's own base entry. That is what
// an add followed by `aiacc remove` leaves behind on purpose, so nothing in it
// is the user's and the duplicate may take the dir. Any other entry — a real
// file or dir, a link pointing elsewhere — keeps the dir refused.
func onlySharedLinks(providerName, dir string, ents []os.DirEntry) bool {
	status := map[string]share.Status{}
	if base, shared, ok := provider.Shared(providerName); ok {
		for _, e := range share.Check(base, dir, shared).Entries {
			status[e.Name] = e.Status
		}
	}
	for _, e := range ents {
		if status[e.Name()] != share.Already {
			return false
		}
	}
	return true
}

// missingDirs lists dir and each missing parent above it, leaf first, up to
// (not including) the first ancestor that exists: what MkdirAll(dir) creates.
func missingDirs(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil || filepath.Dir(d) == d {
			return out
		}
		out = append(out, d)
	}
}

// writeNew writes body to a file that must not exist yet, mode 0600.
func writeNew(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// knownAccounts lists every registered account as provider/account, for a
// refusal that names an account that does not exist.
func knownAccounts(c *config.Config) string {
	var out []string
	for _, pn := range slices.Sorted(maps.Keys(c.Providers)) {
		for _, an := range slices.Sorted(maps.Keys(c.Providers[pn].Accounts)) {
			out = append(out, pn+"/"+an)
		}
	}
	if len(out) == 0 {
		return "none yet (aiacc add)"
	}
	return strings.Join(out, ", ")
}

// duplicateScreen is indirected so tests can drive the picker's duplicate flow
// without a tty.
var duplicateScreen = tui.RunDuplicate

// runDuplicateTUI is the picker's c: the duplicate screen submits each answer
// to duplicateAccount, so a refusal is the CLI's own, shown on the screen with
// nothing written; Esc leaves with nothing written.
func runDuplicateTUI(cfgPath string, row tui.Row) error {
	var d duplicated
	ok, err := duplicateScreen(row.Provider, row.Account, func(name, dir string) error {
		var err error
		d, err = duplicateAccount(cfgPath, row.Provider, row.Account, name, dir)
		return err
	})
	if err != nil || !ok {
		return err
	}
	return showMessage("aiacc — duplicated", d.lines())
}

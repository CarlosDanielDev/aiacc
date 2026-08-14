package tui

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// sample is two switchable claude accounts; "work" is active.
func sample() []Row {
	return []Row{
		{Provider: "claude", Account: "personal", Email: "me@x.com", Dir: "/p", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true},
		{Provider: "claude", Account: "work", Email: "w@co.com", Dir: "/w", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true, Active: true},
	}
}

func drv(t *testing.T, keys string, rows []Row) Result {
	t.Helper()
	res, err := drive(rows, 80, bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	return res
}

func TestDriveArrowUpThenEnter(t *testing.T) {
	// Cursor starts on the active row (1); Up -> 0; Enter switches to 0.
	if res := drv(t, "\x1b[A\r", sample()); res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveDefaultsToActiveRow(t *testing.T) {
	if res := drv(t, "\r", sample()); res.Kind != Switch || res.Index != 1 {
		t.Fatalf("got %+v, want Switch 1 (active row)", res)
	}
}

func TestDriveQuitCancels(t *testing.T) {
	for _, k := range []string{"q", "\x03", "\x1b"} { // q, Ctrl-C, lone Esc
		if res := drv(t, k, sample()); res.Kind != Cancelled {
			t.Fatalf("key %q: got %+v, want Cancelled", k, res)
		}
	}
}

func TestDriveClampsAtTop(t *testing.T) {
	if res := drv(t, "\x1b[A\x1b[A\r", sample()); res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveVimKeys(t *testing.T) {
	if res := drv(t, "jk\r", sample()); res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveAddKey(t *testing.T) {
	if res := drv(t, "a", sample()); res.Kind != Add {
		t.Fatalf("got %+v, want Add", res)
	}
}

// TestDriveEnterOnBlockedDoesNotSwitch: the cursor can now reach a blocked row
// (so it can be removed), but Enter there is inert — you still cannot switch into
// a broken account. The guard moved from the cursor to the action.
func TestDriveEnterOnBlockedDoesNotSwitch(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "ok", Email: "a@b.c", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true}, // switchable (cursor starts here)
		{Provider: "claude", Account: "gone", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false},              // blocked: dir missing
	}
	// Down onto the blocked row, Enter (inert), then q.
	if res := drv(t, "\x1b[B\rq", rows); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled (Enter on blocked must not switch)", res)
	}
}

func TestDriveEnterOnSelectableSwitches(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false},
		{Provider: "claude", Account: "ok", Email: "a@b.c", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true},
	}
	// Cursor starts on the first selectable row (1). Enter switches it.
	if res := drv(t, "\r", rows); res.Kind != Switch || res.Index != 1 {
		t.Fatalf("got %+v, want Switch 1", res)
	}
}

func TestInitialCursorPrefersActiveThenSelectableThenFirst(t *testing.T) {
	rows := []Row{
		{Provider: "p", Account: "blocked", EnvVar: "E", DirExists: false},
		{Provider: "p", Account: "active", Email: "a", EnvVar: "E", DirExists: true, Active: true},
	}
	if got := initialCursor(rows); got != 1 {
		t.Fatalf("initialCursor = %d, want 1 (active)", got)
	}
	// All blocked → row 0, so the list is still navigable for removal.
	if got := initialCursor([]Row{{EnvVar: "", DirExists: true}, {EnvVar: "", DirExists: true}}); got != 0 {
		t.Fatalf("initialCursor(all blocked) = %d, want 0", got)
	}
	if got := initialCursor(nil); got != -1 {
		t.Fatalf("initialCursor(empty) = %d, want -1", got)
	}
}

// --- Remove -------------------------------------------------------------------

func TestDriveRemoveConfirmYes(t *testing.T) {
	// Cursor starts on the active row (1); d opens confirm, y removes it.
	if res := drv(t, "dy", sample()); res.Kind != Remove || res.Index != 1 {
		t.Fatalf("got %+v, want Remove 1", res)
	}
}

func TestDriveRemoveCancelWithN(t *testing.T) {
	// d opens confirm, n backs out to the list, q quits — nothing removed.
	if res := drv(t, "dnq", sample()); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled", res)
	}
}

// TestDriveRemoveEnterIsInert: a destructive remove needs an explicit y — Enter
// in the confirm screen must not remove anything.
func TestDriveRemoveEnterIsInert(t *testing.T) {
	// d confirm, Enter (inert, still confirming), n backs out, q quits.
	if res := drv(t, "d\rnq", sample()); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled (Enter must not confirm removal)", res)
	}
}

func TestDriveRemoveReachesBlockedRow(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "ok", Email: "a@b.c", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true},
		{Provider: "claude", Account: "junk", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false}, // blocked
	}
	// Down onto the blocked junk row, d, y — it can be removed even though it
	// can't be switched into.
	if res := drv(t, "\x1b[Bdy", rows); res.Kind != Remove || res.Index != 1 {
		t.Fatalf("got %+v, want Remove 1 (blocked row removable)", res)
	}
}

func TestRenderRemoveShowsAccount(t *testing.T) {
	r := Row{Provider: "claude", Account: "work"}
	out := RenderRemove(r, 80)
	for _, want := range []string{"remove account", "claude · work", "left on disk", "y", "cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderShowsActiveAndLogin(t *testing.T) {
	out := Render(sample(), 0, 80)
	for _, want := range []string{"▸", "●", "claude · work", "w@co.com", "switch account"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderBlockedBadges(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false},
		{Provider: "foo", Account: "x", EnvVar: "", DirExists: true},
	}
	out := Render(rows, -1, 80)
	for _, want := range []string{"dir missing", "no env var"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing blocked badge %q:\n%s", want, out)
		}
	}
}

func TestRenderEmptyState(t *testing.T) {
	if out := Render(nil, -1, 80); !strings.Contains(out, "No accounts yet") {
		t.Errorf("empty state missing guidance:\n%s", out)
	}
}

// --- Setup gate ---------------------------------------------------------------

func setupInfo() SetupInfo {
	return SetupInfo{Shell: "fish", Line: "aiacc shell-init fish | source", Path: "~/.config/fish/config.fish"}
}

func TestSetupInstallCallsInstallThenExits(t *testing.T) {
	called := false
	install := func() error { called = true; return nil }
	// i triggers install; the done screen then exits on q.
	err := driveSetup(setupInfo(), install, 80, bytes.NewBufferString("iq"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveSetup: %v", err)
	}
	if !called {
		t.Fatal("install was not called on 'i'")
	}
}

func TestSetupQuitSkipsInstall(t *testing.T) {
	called := false
	install := func() error { called = true; return nil }
	if err := driveSetup(setupInfo(), install, 80, bytes.NewBufferString("q"), &bytes.Buffer{}); err != nil {
		t.Fatalf("driveSetup: %v", err)
	}
	if called {
		t.Fatal("install ran on quit — must only run on 'i'")
	}
}

func TestSetupAlreadyPresentDoesNotInstall(t *testing.T) {
	info := setupInfo()
	info.AlreadyPresent = true
	called := false
	install := func() error { called = true; return nil }
	// In the present phase, 'i' is inert; only quit exits.
	if err := driveSetup(info, install, 80, bytes.NewBufferString("iq"), &bytes.Buffer{}); err != nil {
		t.Fatalf("driveSetup: %v", err)
	}
	if called {
		t.Fatal("install ran though the hook was already present")
	}
}

func TestSetupInstallErrorShownNotFatal(t *testing.T) {
	install := func() error { return errors.New("permission denied") }
	// i -> error screen; q exits. driveSetup itself must not return the error.
	if err := driveSetup(setupInfo(), install, 80, bytes.NewBufferString("iq"), &bytes.Buffer{}); err != nil {
		t.Fatalf("driveSetup returned install error, should render it: %v", err)
	}
}

func TestRenderSetupShowsShellLinePath(t *testing.T) {
	out := RenderSetup(setupInfo(), phaseAsk, nil, 80)
	for _, want := range []string{"one-time setup", "fish", "shell-init fish | source", "install it for me"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup render missing %q:\n%s", want, out)
		}
	}
}

// --- Add account --------------------------------------------------------------

func add(t *testing.T, keys string) AddResult {
	t.Helper()
	res, err := driveAdd("", 80, bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	return res
}

func TestAddTypeNameEnter(t *testing.T) {
	res := add(t, "work\r")
	if !res.OK || res.Name != "work" || res.Dir != "~/.claude-work" {
		t.Fatalf("got %+v, want {work ~/.claude-work true}", res)
	}
}

// TestAddRejectsJunkChars is the fix for the "??????????" account: characters
// that are not alias-safe cannot be typed into the name field at all.
func TestAddRejectsJunkChars(t *testing.T) {
	res := add(t, "wo?!$ rk\r") // ? ! $ space are all dropped
	if !res.OK || res.Name != "work" {
		t.Fatalf("got %+v, want name=work (junk filtered)", res)
	}
}

func TestAddEmptyNameBlocksThenAccepts(t *testing.T) {
	// Enter on an empty name is refused; after typing it succeeds.
	res := add(t, "\rok\r")
	if !res.OK || res.Name != "ok" {
		t.Fatalf("got %+v, want name=ok", res)
	}
}

func TestAddEscCancels(t *testing.T) {
	if res := add(t, "work\x1b"); res.OK {
		t.Fatalf("got %+v, want cancelled", res)
	}
}

func TestAddBackspace(t *testing.T) {
	if res := add(t, "work\x7f\r"); !res.OK || res.Name != "wor" {
		t.Fatalf("got %+v, want name=wor", res)
	}
}

func TestAddCustomDirViaTab(t *testing.T) {
	// Tab to the dir field, type an absolute dir; it overrides the default.
	res := add(t, "work\t/tmp/x\r")
	if !res.OK || res.Name != "work" || res.Dir != "/tmp/x" {
		t.Fatalf("got %+v, want dir=/tmp/x", res)
	}
}

func TestRenderAddShowsFieldsAndLogin(t *testing.T) {
	out := RenderAdd("me@x.com", "wo", "", 0, "", 80)
	for _, want := range []string{"add account", "name", "wo", "~/.claude-wo", "me@x.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("add render missing %q:\n%s", want, out)
		}
	}
}

// --- Layout self-test ---------------------------------------------------------

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleLen(s string) int { return utf8.RuneCountInString(ansi.ReplaceAllString(s, "")) }

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

// TestFramesStaySquare is a poka-yoke self-test on the layout: no width, and no
// row content, may tear a box — for the picker or the gate. Every framed line
// must have the same visible width, at a width too small for the content and one
// far wider than it.
func TestFramesStaySquare(t *testing.T) {
	rows := append(sample(),
		Row{Provider: "claude", Account: "averylongaccountnamethatwouldoverflowanyreasonableframe", Email: "someone.with.a.long.address@example.com", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true},
		Row{Provider: "foo", Account: "gone", EnvVar: "", DirExists: false},
	)
	frames := map[string]string{}
	for _, cols := range []int{20, 40, 80, 200} {
		frames["picker"] = Render(rows, 0, cols)
		frames["setupAsk"] = RenderSetup(setupInfo(), phaseAsk, nil, cols)
		frames["setupDone"] = RenderSetup(setupInfo(), phaseDone, nil, cols)
		frames["add"] = RenderAdd("me@example.com", "work", "", 0, "bad name", cols)
		frames["remove"] = RenderRemove(Row{Provider: "claude", Account: "work"}, cols)
		for name, out := range frames {
			wid := -1
			for _, ln := range strings.Split(out, "\r\n") {
				trimmed := strings.TrimLeft(ansi.ReplaceAllString(ln, ""), " ")
				if !strings.ContainsAny(firstRune(trimmed), "╭│├╰") {
					continue
				}
				if n := visibleLen(ln); wid == -1 {
					wid = n
				} else if n != wid {
					t.Fatalf("%s cols=%d: framed line width %d != %d:\n%q", name, cols, n, wid, ln)
				}
			}
			if wid == -1 {
				t.Fatalf("%s cols=%d: no framed lines found", name, cols)
			}
		}
	}
}

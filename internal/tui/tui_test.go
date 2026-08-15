package tui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// sample is two launchable claude profiles.
func sample() []Row {
	return []Row{
		{Provider: "claude", Account: "personal", Email: "me@x.com", Dir: "/p", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		{Provider: "claude", Account: "work", Email: "w@co.com", Dir: "/w", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
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

func TestDriveEnterLaunchesFirst(t *testing.T) {
	if res := drv(t, "\r", sample()); res.Kind != Launch || res.Index != 0 {
		t.Fatalf("got %+v, want Launch 0", res)
	}
}

func TestDriveDownThenLaunch(t *testing.T) {
	if res := drv(t, "\x1b[B\r", sample()); res.Kind != Launch || res.Index != 1 {
		t.Fatalf("got %+v, want Launch 1", res)
	}
}

func TestDriveQuitCancels(t *testing.T) {
	for _, k := range []string{"q", "\x03", "\x1b"} { // q, Ctrl-C, lone Esc
		if res := drv(t, k, sample()); res.Kind != Cancelled {
			t.Fatalf("key %q: got %+v, want Cancelled", k, res)
		}
	}
}

func TestDriveVimKeys(t *testing.T) {
	if res := drv(t, "j\r", sample()); res.Kind != Launch || res.Index != 1 {
		t.Fatalf("got %+v, want Launch 1", res)
	}
}

func TestDriveClampsAtBottom(t *testing.T) {
	// Down past the last row stays put; Enter launches it.
	if res := drv(t, "\x1b[B\x1b[B\r", sample()); res.Kind != Launch || res.Index != 1 {
		t.Fatalf("got %+v, want Launch 1", res)
	}
}

func TestDriveAddKey(t *testing.T) {
	if res := drv(t, "a", sample()); res.Kind != Add {
		t.Fatalf("got %+v, want Add", res)
	}
}

// TestDriveEnterOnBlockedDoesNotLaunch: the cursor can reach a blocked row (so it
// can be removed), but Enter there is inert — you can't launch a broken profile.
func TestDriveEnterOnBlockedDoesNotLaunch(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "ok", Email: "a@b.c", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		{Provider: "claude", Account: "gone", DirExists: false, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"}, // dir missing
	}
	if res := drv(t, "\x1b[B\rq", rows); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled (Enter on blocked must not launch)", res)
	}
}

func TestInitialCursorFirstLaunchableThenFirstThenEmpty(t *testing.T) {
	rows := []Row{
		{Provider: "p", Account: "blocked", DirExists: false, EnvVar: "E", Command: "c"},
		{Provider: "p", Account: "ok", DirExists: true, EnvVar: "E", Command: "c"},
	}
	if got := initialCursor(rows); got != 1 {
		t.Fatalf("initialCursor = %d, want 1 (first launchable)", got)
	}
	if got := initialCursor([]Row{{DirExists: false, EnvVar: "E", Command: "c"}, {DirExists: false}}); got != 0 {
		t.Fatalf("initialCursor(all blocked) = %d, want 0", got)
	}
	if got := initialCursor(nil); got != -1 {
		t.Fatalf("initialCursor(empty) = %d, want -1", got)
	}
}

// --- Remove -------------------------------------------------------------------

func TestDriveRemoveConfirmYes(t *testing.T) {
	if res := drv(t, "dy", sample()); res.Kind != Remove || res.Index != 0 {
		t.Fatalf("got %+v, want Remove 0", res)
	}
}

func TestDriveRemoveCancelWithN(t *testing.T) {
	if res := drv(t, "dnq", sample()); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled", res)
	}
}

// TestDriveRemoveEnterIsInert: removal needs an explicit y — Enter must not
// confirm.
func TestDriveRemoveEnterIsInert(t *testing.T) {
	if res := drv(t, "d\rnq", sample()); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled (Enter must not confirm removal)", res)
	}
}

func TestDriveRemoveReachesBlockedRow(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "ok", Email: "a@b.c", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		{Provider: "claude", Account: "junk", DirExists: false, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"}, // blocked
	}
	if res := drv(t, "\x1b[Bdy", rows); res.Kind != Remove || res.Index != 1 {
		t.Fatalf("got %+v, want Remove 1 (blocked row removable)", res)
	}
}

func TestRenderRemoveShowsAccount(t *testing.T) {
	out := RenderRemove(Row{Provider: "claude", Account: "work"}, 80)
	for _, want := range []string{"remove profile", "work", "left on disk", "y", "cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove render missing %q:\n%s", want, out)
		}
	}
}

// --- Render -------------------------------------------------------------------

func TestRenderShowsProfilesAndLogin(t *testing.T) {
	out := Render(sample(), 0, 80)
	for _, want := range []string{"▸", "work", "w@co.com", "launch a profile"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderBlockedBadges(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", DirExists: false, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"}, // dir missing
		{Provider: "openai", Account: "x", DirExists: true, EnvVar: "OPENAI_CONFIG", Command: ""},               // no launcher
	}
	out := Render(rows, -1, 80)
	for _, want := range []string{"dir missing", "no launcher"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing blocked badge %q:\n%s", want, out)
		}
	}
}

func TestRenderEmptyState(t *testing.T) {
	if out := Render(nil, -1, 80); !strings.Contains(out, "No profiles yet") {
		t.Errorf("empty state missing guidance:\n%s", out)
	}
}

// --- Add profile --------------------------------------------------------------

func add(t *testing.T, keys string) AddResult {
	t.Helper()
	res, err := driveAdd("", 80, bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	return res
}

func TestAddTypeNameEnter(t *testing.T) {
	res := add(t, "claude-work\r")
	if !res.OK || res.Name != "claude-work" || res.Dir != "~/.claude-claude-work" {
		t.Fatalf("got %+v", res)
	}
}

// TestAddRejectsJunkChars is the fix for the "??????????" profile: characters
// that aren't command-safe cannot be typed into the name field at all.
func TestAddRejectsJunkChars(t *testing.T) {
	res := add(t, "wo?!$ rk\r")
	if !res.OK || res.Name != "work" {
		t.Fatalf("got %+v, want name=work (junk filtered)", res)
	}
}

func TestAddEmptyNameBlocksThenAccepts(t *testing.T) {
	if res := add(t, "\rok\r"); !res.OK || res.Name != "ok" {
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
	res := add(t, "work\t/tmp/x\r")
	if !res.OK || res.Name != "work" || res.Dir != "/tmp/x" {
		t.Fatalf("got %+v, want dir=/tmp/x", res)
	}
}

func TestRenderAddShowsFieldsAndLogin(t *testing.T) {
	out := RenderAdd("me@x.com", "wo", "", 0, "", 80)
	for _, want := range []string{"add profile", "name", "wo", "~/.claude-wo", "me@x.com", "launches as: wo"} {
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
// row content, may tear a box. Every framed line must have the same visible
// width, at a width too small for the content and one far wider than it.
func TestFramesStaySquare(t *testing.T) {
	rows := append(sample(),
		Row{Provider: "claude", Account: "averylongaccountnamethatwouldoverflowanyreasonableframe", Email: "someone.with.a.long.address@example.com", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		Row{Provider: "openai", Account: "gone", DirExists: false, EnvVar: "", Command: ""},
	)
	frames := map[string]string{}
	for _, cols := range []int{20, 40, 80, 200} {
		frames["picker"] = Render(rows, 0, cols)
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

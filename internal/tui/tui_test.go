package tui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// sample is two switchable claude accounts; "work" is active and has a quota.
func sample() []Row {
	return []Row{
		{Provider: "claude", Account: "personal", Email: "me@x.com", Dir: "/p", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true, Tokens: 340_000},
		{Provider: "claude", Account: "work", Email: "w@co.com", Dir: "/w", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true, Active: true, Tokens: 1_200_000, Quota: 2_000_000},
	}
}

func drv(t *testing.T, keys string, rows []Row) Result {
	t.Helper()
	res, err := drive(rows, true, 80, bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	return res
}

func TestDriveArrowUpThenEnter(t *testing.T) {
	// Cursor starts on the active row (1); Up -> 0; Enter switches to 0.
	res := drv(t, "\x1b[A\r", sample())
	if res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveDefaultsToActiveRow(t *testing.T) {
	res := drv(t, "\r", sample())
	if res.Kind != Switch || res.Index != 1 {
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
	res := drv(t, "\x1b[A\x1b[A\r", sample()) // Up past the top stays at 0
	if res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveVimKeys(t *testing.T) {
	// j from active(1) clamps at the bottom; k -> 0; enter.
	res := drv(t, "jk\r", sample())
	if res.Kind != Switch || res.Index != 0 {
		t.Fatalf("got %+v, want Switch 0", res)
	}
}

func TestDriveAddKey(t *testing.T) {
	if res := drv(t, "a", sample()); res.Kind != Add {
		t.Fatalf("got %+v, want Add", res)
	}
}

// TestDriveSkipsBlockedRows is the core poka-yoke: the cursor cannot land on a
// row that is unsafe to switch into, so Enter can never return one.
func TestDriveSkipsBlockedRows(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false},              // blocked: dir missing
		{Provider: "claude", Account: "ok", Email: "a@b.c", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true}, // switchable
		{Provider: "foo", Account: "x", EnvVar: "", DirExists: true},                                      // blocked: no env var
	}
	// Cursor starts on the only selectable row (1). Down and Up both have only
	// blocked rows to move onto, so they must stay put.
	if res := drv(t, "\x1b[B\x1b[A\r", rows); res.Kind != Switch || res.Index != 1 {
		t.Fatalf("got %+v, want Switch 1 (blocked rows unreachable)", res)
	}
}

// TestDriveAllBlockedCannotSwitch: with nothing selectable, Enter is a no-op and
// only quit gets out.
func TestDriveAllBlockedCannotSwitch(t *testing.T) {
	rows := []Row{{Provider: "foo", Account: "x", EnvVar: "", DirExists: true}}
	if res := drv(t, "\rq", rows); res.Kind != Cancelled { // Enter no-op, then q
		t.Fatalf("got %+v, want Cancelled", res)
	}
}

func TestInitialCursorSkipsBlockedToActive(t *testing.T) {
	rows := []Row{
		{Provider: "p", Account: "blocked", EnvVar: "E", DirExists: false},
		{Provider: "p", Account: "active", Email: "a", EnvVar: "E", DirExists: true, Active: true},
	}
	if got := initialCursor(rows); got != 1 {
		t.Fatalf("initialCursor = %d, want 1", got)
	}
	if got := initialCursor([]Row{{EnvVar: "", DirExists: true}}); got != -1 {
		t.Fatalf("initialCursor(all blocked) = %d, want -1", got)
	}
}

func TestRenderShowsActiveTokensLogin(t *testing.T) {
	out := Render(sample(), 0, true, 80)
	for _, want := range []string{"▸", "● ACTIVE", "1.2M", "340.0k", "w@co.com", "aiacc"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderAndonOnlyWhenHookInactive(t *testing.T) {
	const andon = "hook not active"
	if in := Render(sample(), 0, false, 80); !strings.Contains(in, andon) {
		t.Errorf("andon missing when hook inactive:\n%s", in)
	}
	if act := Render(sample(), 0, true, 80); strings.Contains(act, andon) {
		t.Errorf("andon shown when hook active:\n%s", act)
	}
}

func TestRenderBlockedBadges(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: false},
		{Provider: "foo", Account: "x", EnvVar: "", DirExists: true},
	}
	out := Render(rows, -1, true, 80)
	for _, want := range []string{"dir missing", "no env var"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing blocked badge %q:\n%s", want, out)
		}
	}
}

func TestRenderEmptyState(t *testing.T) {
	if out := Render(nil, -1, true, 80); !strings.Contains(out, "No accounts registered") {
		t.Errorf("empty state missing guidance:\n%s", out)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleLen(s string) int { return utf8.RuneCountInString(ansi.ReplaceAllString(s, "")) }

// TestRenderFrameStaysSquare is a poka-yoke self-test on the layout: no width,
// and no row content, may tear the box. Every framed line (border or body) must
// have the same visible width — even at a width too small to fit the content and
// one far wider than it.
func TestRenderFrameStaysSquare(t *testing.T) {
	rows := append(sample(),
		Row{Provider: "claude", Account: "averylongaccountnamethatwouldoverflowanyreasonableframe", Email: "someone.with.a.long.address@example.com", EnvVar: "CLAUDE_CONFIG_DIR", DirExists: true, Tokens: 999_999_999},
		Row{Provider: "foo", Account: "gone", EnvVar: "", DirExists: false},
	)
	for _, cols := range []int{20, 40, 80, 200} {
		out := Render(rows, 0, false, cols) // hook inactive => andon rows too
		var wid = -1
		for _, ln := range strings.Split(out, "\r\n") {
			trimmed := strings.TrimLeft(ansi.ReplaceAllString(ln, ""), " ")
			if !strings.ContainsAny(firstRune(trimmed), "╭│├╰") {
				continue
			}
			n := visibleLen(ln)
			if wid == -1 {
				wid = n
			} else if n != wid {
				t.Fatalf("cols=%d: framed line width %d != %d:\n%q", cols, n, wid, ln)
			}
		}
		if wid == -1 {
			t.Fatalf("cols=%d: no framed lines found", cols)
		}
	}
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

func TestHumanTokens(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1500: "1.5k", 340_000: "340.0k", 2_500_000: "2.5M"}
	for in, want := range cases {
		if got := humanTokens(in); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

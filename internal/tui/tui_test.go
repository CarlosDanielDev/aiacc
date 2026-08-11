package tui

import (
	"bytes"
	"strings"
	"testing"
)

func sample() []Row {
	return []Row{
		{Provider: "claude", Account: "personal", Dir: "/p", Tokens: 340_000},
		{Provider: "claude", Account: "work", Dir: "/w", Active: true, Tokens: 1_200_000},
	}
}

func TestSelectArrowUpThenEnter(t *testing.T) {
	// Cursor starts on the active row (index 1); Up -> 0; Enter selects 0.
	in := bytes.NewBufferString("\x1b[A\r")
	idx, err := Select(sample(), in, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestSelectDefaultsToActiveRow(t *testing.T) {
	in := bytes.NewBufferString("\r") // enter immediately
	idx, _ := Select(sample(), in, &bytes.Buffer{})
	if idx != 1 {
		t.Fatalf("idx = %d, want 1 (active row)", idx)
	}
}

func TestSelectQuitCancels(t *testing.T) {
	for _, k := range []string{"q", "\x03"} { // q, Ctrl-C
		idx, _ := Select(sample(), bytes.NewBufferString(k), &bytes.Buffer{})
		if idx != -1 {
			t.Fatalf("key %q: idx = %d, want -1", k, idx)
		}
	}
}

func TestSelectClampsAtTop(t *testing.T) {
	// Up from active(1)->0, Up again stays 0 (clamp), Enter.
	in := bytes.NewBufferString("\x1b[A\x1b[A\r")
	idx, _ := Select(sample(), in, &bytes.Buffer{})
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestSelectVimKeys(t *testing.T) {
	// j from active(1) clamps at bottom, k -> 0, enter.
	in := bytes.NewBufferString("jk\r")
	idx, _ := Select(sample(), in, &bytes.Buffer{})
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestRenderShowsCursorActiveTokens(t *testing.T) {
	out := Render(sample(), 0)
	if !strings.Contains(out, "▸") {
		t.Error("missing cursor marker")
	}
	if !strings.Contains(out, "*") {
		t.Error("missing active marker")
	}
	if !strings.Contains(out, "1.2M") || !strings.Contains(out, "340.0k") {
		t.Errorf("tokens not humanized:\n%s", out)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1500: "1.5k", 340_000: "340.0k", 2_500_000: "2.5M"}
	for in, want := range cases {
		if got := humanTokens(in); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

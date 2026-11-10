package tui

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// animRows is one launchable row (the cursor starts on it) plus a plain row so
// the arrow keys have somewhere to go.
func animRows() []Row {
	return []Row{
		{Provider: "p", Account: "a", Dir: "/tmp/a", DirExists: true, EnvVar: "X", Command: "cli"},
		{Provider: "p", Account: "b", Dir: "/tmp/b"},
	}
}

// startAnimate runs the real animated loop over the read end of a pipe, with
// output discarded, and reports the Result when it returns.
func startAnimate(t *testing.T, r *os.File) <-chan Result {
	t.Helper()
	resCh := make(chan Result, 1)
	go func() {
		res, _ := animateKeys(animRows(), false, "", fixedGeom(80, 24), r, io.Discard)
		resCh <- res
	}()
	return resCh
}

// nextScreenKeys reads from r the way the next screen's loop does — a fresh
// screenLoop — and records every key until end of input.
func nextScreenKeys(t *testing.T, r *os.File) []key {
	t.Helper()
	var got []key
	// The next screen reads the way production does: through a timedTTY shared
	// by the reader and the decoder (see Run and the Run* wrappers).
	src := &timedTTY{File: r}
	err := screenLoop(fixedGeom(80, 24), r, io.Discard,
		func(rb *bufio.Reader) (key, error) { return readKey(rb, src) },
		func(Size) string { return "" },
		func(k key) bool { got = append(got, k); return false })
	if err != nil {
		t.Fatalf("next screen loop: %v", err)
	}
	return got
}

// TestAnimateHandsOffTty: however the animated screen ends, its key-reader
// goroutine must be gone before animate returns. The reader and the next
// screen's loop share the tty's input queue; a reader left behind races the
// next loop for the first keypress and swallows it (issue #136).
func TestAnimateHandsOffTty(t *testing.T) {
	cases := map[string]struct {
		trigger  string // the key that ends the animated screen
		follow   string // a key typed during the transition to the next screen
		want     Result
		wantKeys []key // what the next screen's loop must receive, in order
	}{
		"quit":          {trigger: "q", follow: "a", want: Result{Kind: Cancelled}, wantKeys: []key{keyAdd}},
		"launch":        {trigger: "\r", follow: "a", want: Result{Kind: Launch, Index: 0}, wantKeys: []key{keyAdd}},
		"edit-then-esc": {trigger: "e", follow: "\x1b", want: Result{Kind: Edit, Index: 0}, wantKeys: []key{keyQuit}},
		// An arrow must reach the next screen as one key, never as the stray
		// "[B" text a mid-sequence handoff would leave in a field.
		"arrow-unsplit": {trigger: "q", follow: "\x1b[B", want: Result{Kind: Cancelled}, wantKeys: []key{keyDown}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			resCh := startAnimate(t, r)
			if _, err := io.WriteString(w, tc.trigger); err != nil {
				t.Fatal(err)
			}
			select {
			case res := <-resCh:
				if res != tc.want {
					t.Fatalf("animate returned %+v, want %+v", res, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("animate did not return")
			}

			// animate is done — the follow-up byte belongs to the next screen.
			if _, err := io.WriteString(w, tc.follow); err != nil {
				t.Fatal(err)
			}
			w.Close() // end of input ends the next screen's loop

			got := nextScreenKeys(t, r)
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("next screen saw %v keys (%v), want %v", len(got), got, tc.wantKeys)
			}
			for i, k := range tc.wantKeys {
				if got[i] != k {
					t.Fatalf("next screen key %d = %v, want %v (all: %v)", i, got[i], k, got)
				}
			}
		})
	}
}

// listRows is a list whose focused row overflows its column (the marquee has
// something to scroll) plus a short row for the arrow keys.
func listRows() []ListItem {
	return []ListItem{
		{Primary: "a very long session title that overflows the primary column"},
		{Primary: "short"},
	}
}

// startList runs the real selection loop over the read end of a pipe, with
// output discarded, and reports the chosen index when it returns.
func startList(t *testing.T, r *os.File) <-chan int {
	t.Helper()
	resCh := make(chan int, 1)
	go func() {
		i, _ := driveList("pick", listRows(), fixedGeom(80, 24), r, io.Discard)
		resCh <- i
	}()
	return resCh
}

// TestListHandsOffTty: however the selection list ends, its key-reader
// goroutine must be gone before the loop returns — the same contract
// TestAnimateHandsOffTty pins for the launcher (issue #136, extended to lists
// by #140's ticker). The reader and the next screen's loop share the tty's
// input queue; a reader left behind races the next loop for the first
// keypress and swallows it.
func TestListHandsOffTty(t *testing.T) {
	cases := map[string]struct {
		trigger  string // the key that ends the list
		follow   string // a key typed during the transition to the next screen
		want     int
		wantKeys []key // what the next screen's loop must receive, in order
	}{
		"quit":          {trigger: "q", follow: "a", want: -1, wantKeys: []key{keyAdd}},
		"select":        {trigger: "\r", follow: "a", want: 0, wantKeys: []key{keyAdd}},
		"arrow-unsplit": {trigger: "q", follow: "\x1b[B", want: -1, wantKeys: []key{keyDown}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			resCh := startList(t, r)
			if _, err := io.WriteString(w, tc.trigger); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-resCh:
				if got != tc.want {
					t.Fatalf("list returned %d, want %d", got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("list did not return")
			}

			// The list is done — the follow-up byte belongs to the next screen.
			if _, err := io.WriteString(w, tc.follow); err != nil {
				t.Fatal(err)
			}
			w.Close() // end of input ends the next screen's loop

			got := nextScreenKeys(t, r)
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("next screen saw %v keys (%v), want %v", len(got), got, tc.wantKeys)
			}
			for i, k := range tc.wantKeys {
				if got[i] != k {
					t.Fatalf("next screen key %d = %v, want %v (all: %v)", i, got[i], k, got)
				}
			}
		})
	}
}

// TestListRepaintsBetweenKeys: the list loop repaints on its ticker without a
// keypress, and the focused row's marquee advances between those repaints
// (issue #140).
func TestListRepaintsBetweenKeys(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var out bytes.Buffer // read only after the loop returns — no race
	resCh := make(chan int, 1)
	go func() {
		i, _ := driveList("pick", listRows(), fixedGeom(80, 24), r, &out)
		resCh <- i
	}()
	time.Sleep(300 * time.Millisecond) // ~3 ticks at the 90ms the launcher uses
	if _, err := io.WriteString(w, "q"); err != nil {
		t.Fatal(err)
	}
	w.Close() // end of input ends the loop
	if i := <-resCh; i != -1 {
		t.Fatalf("quit → %d, want -1", i)
	}

	// The focused row ("> …") must have rendered in more than one phase while
	// no key was pressed.
	focused := map[string]bool{}
	for _, ln := range strings.Split(ansi.ReplaceAllString(out.String(), ""), "\r\n") {
		if strings.Contains(ln, "> ") {
			focused[ln] = true
		}
	}
	if len(focused) < 2 {
		t.Fatalf("list rendered %d distinct focused-row variants between keypresses, want ≥ 2:\n%s", len(focused), out.String())
	}
}

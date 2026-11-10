package tui

import (
	"bufio"
	"io"
	"os"
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

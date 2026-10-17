package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// stubTerminal swaps openTerminal for one that counts restores, and returns the
// counter. open is what the stub returns on each call.
func stubTerminal(t *testing.T, open func() error) *int {
	t.Helper()
	restores := 0
	old := openTerminal
	openTerminal = func() (*os.File, *geom, func(), error) {
		if err := open(); err != nil {
			return nil, nil, nil, err
		}
		return nil, fixedGeom(80, 24), func() { restores++ }, nil
	}
	t.Cleanup(func() { openTerminal = old })
	return &restores
}

// TestOnTTYRestoresOnEveryReturn: whatever fn returns, the terminal is restored
// exactly once, so no screen can leave the user in raw mode.
func TestOnTTYRestoresOnEveryReturn(t *testing.T) {
	errBoom := errors.New("boom")
	cases := map[string]func() (int, error){
		"normal": func() (int, error) { return 1, nil },
		"error":  func() (int, error) { return 0, errBoom },
	}
	for name, fn := range cases {
		restores := stubTerminal(t, func() error { return nil })
		_, _ = onTTY(-1, func(*os.File, *geom) (int, error) { return fn() })
		if *restores != 1 {
			t.Fatalf("%s: restore ran %d times, want 1", name, *restores)
		}
	}
}

// TestOnTTYOpenFailureRestoresNothing: when the terminal cannot be opened the
// caller gets the zero value back, and there is nothing to restore.
func TestOnTTYOpenFailureRestoresNothing(t *testing.T) {
	errOpen := errors.New("no tty")
	restores := stubTerminal(t, func() error { return errOpen })
	ran := false
	res, err := onTTY(-1, func(*os.File, *geom) (int, error) { ran = true; return 7, nil })
	if !errors.Is(err, errOpen) || res != -1 {
		t.Fatalf("got (%d, %v), want (-1, %v)", res, err, errOpen)
	}
	if ran || *restores != 0 {
		t.Fatalf("screen ran=%v restores=%d after a failed open", ran, *restores)
	}
}

// TestKeyExitsRestoreTheTerminal: each key that ends a list (Esc, Ctrl-C, q) and
// end of input all return through onTTY, so each restores the terminal once. The
// Ctrl-C and q inputs are followed by Enter: if the key failed to end the screen,
// Enter would select row 0 and the test would fail. A lone Esc has to be last,
// because a byte after it reads as the start of an arrow sequence.
func TestKeyExitsRestoreTheTerminal(t *testing.T) {
	for name, keys := range map[string]string{"esc": "\x1b", "ctrl-c": "\x03\r", "q": "q\r", "eof": ""} {
		restores := stubTerminal(t, func() error { return nil })
		idx, err := onTTY(-1, func(_ *os.File, g *geom) (int, error) {
			return driveList("t", []ListItem{{Primary: "a"}, {Primary: "b"}}, g, strings.NewReader(keys), io.Discard)
		})
		if err != nil || idx != -1 {
			t.Fatalf("%s: got (%d, %v), want (-1, nil)", name, idx, err)
		}
		if *restores != 1 {
			t.Fatalf("%s: restore ran %d times, want 1", name, *restores)
		}
	}
}

// TestTextFormsCtrlCCancels: Ctrl-C cancels the three byte-driven text forms
// without a result. Each input has a follow-up that would be accepted if the
// key were ignored (a name, a rename, a save), so the test fails in that case.
func TestTextFormsCtrlCCancels(t *testing.T) {
	g := fixedGeom(80, 24)
	if res, err := driveAdd("", "claude", false, g, strings.NewReader("\x03ab\r"), io.Discard); err != nil || res != (AddResult{}) {
		t.Fatalf("add: got (%+v, %v), want zero result", res, err)
	}
	if name, err := driveRename("work", nil, g, strings.NewReader("\x03zz\r"), io.Discard); err != nil || name != "" {
		t.Fatalf("rename: got (%q, %v), want empty", name, err)
	}
	path := editFixture(t, `{"env":{}}`)
	if saved, err := driveEdit(path, "glm", glmForm(false), g, strings.NewReader("\x03\r"), io.Discard); err != nil || saved {
		t.Fatalf("edit: got (%v, %v), want not saved", saved, err)
	}
}

// TestOnTTYRestoresOnPanic: a panic inside the screen still restores the
// terminal on the way out, so a crash mid-screen cannot leave raw mode behind.
func TestOnTTYRestoresOnPanic(t *testing.T) {
	restores := stubTerminal(t, func() error { return nil })
	func() {
		defer func() { _ = recover() }()
		_, _ = onTTY(-1, func(*os.File, *geom) (int, error) { panic("screen crashed") })
	}()
	if *restores != 1 {
		t.Fatalf("restore ran %d times after a panic, want 1", *restores)
	}
}

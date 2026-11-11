//go:build darwin

package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// TestAnimateHandsOffRealTTY pins issue #146 on the one file it lived on —
// /dev/tty, the way openRawTTY opens it. Go read deadlines do not wake a
// blocking read on a macOS controlling terminal (golang/go#43716), so only
// this file shape can catch the handoff bug: a keypress typed right after a
// screen transition was consumed by the outgoing screen's reader goroutine,
// which is why the owner had to press every action twice.
//
// The driver (see tools/tui-pty-driver.py) writes 'e' into the terminal,
// waits for PHASE1-OK on this process's output, then writes \r. The first
// screen must return Edit, and the next screen must see exactly one Enter.
//
// Env-gated (TUI_PTY_TEST=1) and run from a pty-driving wrapper, NOT from
// `go test` directly: it needs a controlling terminal, and under machine load
// the wrapper's own pty writes can be lost upstream of the code under test —
// a retrying driver is what makes the verdict meaningful.
func TestAnimateHandsOffRealTTY(t *testing.T) {
	if os.Getenv("TUI_PTY_TEST") != "1" {
		t.Skip("manual: set TUI_PTY_TEST=1 and run under tools/tui-pty-driver.py")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no controlling terminal: %v", err)
	}
	defer tty.Close()
	if err := stty(tty, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		t.Skipf("stty raw: %v", err)
	}
	// The driver waits for this before typing 'e': a key written before raw
	// mode sits line-buffered in the canonical discipline, never delivered.
	fmt.Println("READY")

	resCh := make(chan Result, 1)
	go func() {
		res, _ := animateKeys(animRows(), false, "", fixedGeom(80, 24), tty, io.Discard)
		resCh <- res
	}()
	select {
	case res := <-resCh:
		if res.Kind != Edit {
			t.Fatalf("first key: got Kind %v, want Edit", res.Kind)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("launcher never returned on 'e'")
	}
	// Signal the driver to type the follow-up key NOW — mid-join, in the
	// window the outgoing reader is being cancelled in.
	fmt.Println("PHASE1-OK")

	// The next screen's loop, exactly as production opens it: its OWN
	// /dev/tty fd (openRawTTY reopens per screen). The outgoing fd must not
	// be reused here — a 0-byte timed read (the join's arm, the Esc window)
	// leaves that fd's kqueue note in EOF state on macOS, and reopening is
	// precisely what production does about it.
	next, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("reopen /dev/tty: %v", err)
	}
	defer next.Close()
	follow := make(chan key, 1)
	go func() {
		err := screenLoop(fixedGeom(80, 24), next, io.Discard,
			func(rb *bufio.Reader) (key, error) { return readKey(rb, &timedTTY{File: next}) },
			func(Size) string { return "" },
			func(k key) bool { follow <- k; return false })
		if err != nil {
			t.Logf("next screen: %v", err)
		}
	}()
	select {
	case k := <-follow:
		if k != keyEnter {
			t.Fatalf("next screen first key: got %v, want enter", k)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("next screen never received the follow-up keypress — it was eaten by the outgoing screen's reader")
	}
}

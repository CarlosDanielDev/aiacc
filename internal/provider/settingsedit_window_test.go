package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAbortSettingsWritesWaitsForTempRegistration: a shutdown abort that lands
// between creating a write's temp file and registering it must wait for the
// registration, so the temp it removes is never one it did not know about. The
// abort goroutine is started in the create window and must still be blocked
// when the window closes. The write waits for the abort to finish before it
// renames, so the temp is gone and the write is refused. Mutation check: drop
// the Guard in writeSettings and the abort finishes inside the window, which
// fails the test.
func TestAbortSettingsWritesWaitsForTempRegistration(t *testing.T) {
	const body = `{"env":{"ANTHROPIC_BASE_URL":"https://old"}}`
	path := settingsFixture(t, body, 0o600)
	dir := filepath.Dir(path)

	oldCreate, oldWrite := SettingsCreateWindow, SettingsWriteWindow
	t.Cleanup(func() {
		SettingsCreateWindow, SettingsWriteWindow = oldCreate, oldWrite
		ResetSettingsWritesForTest()
	})

	var finished chan struct{}
	SettingsCreateWindow = func(tmp string) {
		finished = make(chan struct{})
		go func() { AbortSettingsWrites(); close(finished) }()
		select {
		case <-finished:
			t.Fatalf("abort ran between creating %s and registering it", filepath.Base(tmp))
		case <-time.After(50 * time.Millisecond):
		}
	}
	SettingsWriteWindow = func() {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("abort never ran")
		}
	}

	err := EditSettings(path, SettingsEdit{Token: sentinelToken})
	if err == nil {
		t.Fatal("write completed after an abort")
	}
	if strings.Contains(err.Error(), sentinelToken) {
		t.Fatalf("error leaks the token: %q", err)
	}
	if n := tempCount(t, dir); n != 0 {
		t.Fatalf("%d temp file(s) left behind after abort", n)
	}
	if raw, _ := os.ReadFile(path); string(raw) != body {
		t.Fatal("aborted write changed the settings file")
	}
}

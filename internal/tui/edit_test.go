package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// sentinelToken stands in for a real key. It must never appear in a frame, a
// hint, or a test failure message.
const sentinelToken = "sk-SENTINEL-123"

// editFixture writes body to a fresh settings.json in a temp dir and returns it.
func editFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runEdit drives the edit screen over keys against the file at path and returns
// whether it saved, plus every byte the screen drew.
func runEdit(t *testing.T, path string, form EditForm, keys string) (bool, string) {
	t.Helper()
	var out bytes.Buffer
	saved, err := driveEdit(path, "glm", form, fixedGeom(80, 24), bytes.NewBufferString(keys), &out)
	if err != nil {
		t.Fatalf("driveEdit: %v", err)
	}
	return saved, out.String()
}

// fileEnv returns the env block of the settings file at path.
func fileEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings file is not valid JSON: %v", err)
	}
	return doc.Env
}

// glmForm is the prefill the picker passes for a settings-backed account.
func glmForm(hasToken bool) EditForm {
	return EditForm{BaseURL: "https://glm.example/api", Model: "glm-4.6", HasToken: hasToken}
}

func TestDriveEditKeyOpensEditForSelectedRow(t *testing.T) {
	res := drv(t, "j"+"e", sample())
	if res.Kind != Edit || res.Index != 1 {
		t.Fatalf("e on row 1: got kind %v index %d, want Edit on 1", res.Kind, res.Index)
	}
}

// TestEditTypesTokenMasked: the typed key is written to the file, and it never
// shows on screen, not even as the typing happens.
func TestEditTypesTokenMasked(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://glm.example/api"}}`)
	saved, screen := runEdit(t, path, glmForm(false), sentinelToken+"\r")
	if !saved {
		t.Fatal("save not reported")
	}
	if got := fileEnv(t, path)["ANTHROPIC_AUTH_TOKEN"]; got != sentinelToken {
		t.Fatalf("token not written")
	}
	if strings.Contains(screen, sentinelToken) {
		t.Fatal("typed token echoed on screen")
	}
	if !strings.Contains(screen, "•") {
		t.Fatal("token field not rendered as dots")
	}
}

// TestEditEmptyTokenKeepsExistingKey: enter on the untouched token field keeps
// the key already in the file, and the base URL edit still lands.
func TestEditEmptyTokenKeepsExistingKey(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"`+sentinelToken+`","ANTHROPIC_BASE_URL":"https://old"}}`)
	// Tab to base URL, clear it ("https://old" is 11 bytes), type the new one.
	keys := "\t" + strings.Repeat("\x7f", len("https://old")) + "https://new\r"
	saved, screen := runEdit(t, path, EditForm{BaseURL: "https://old", HasToken: true}, keys)
	if !saved {
		t.Fatal("save not reported")
	}
	env := fileEnv(t, path)
	if env["ANTHROPIC_AUTH_TOKEN"] != sentinelToken {
		t.Fatal("empty token field cleared the existing key")
	}
	if env["ANTHROPIC_BASE_URL"] != "https://new" {
		t.Fatalf("base URL = %q, want https://new", env["ANTHROPIC_BASE_URL"])
	}
	if strings.Contains(screen, sentinelToken) {
		t.Fatal("existing token displayed on screen")
	}
}

// TestEditRefusedSaveStaysOnScreen: a file that is not valid JSON is refused
// with its name in the hint, nothing is written, and the screen stays open
// until the user leaves. The file's contents, which hold the token, never show.
func TestEditRefusedSaveStaysOnScreen(t *testing.T) {
	bad := `{"env":{"ANTHROPIC_AUTH_TOKEN":"` + sentinelToken + `",,,`
	path := editFixture(t, bad)
	saved, screen := runEdit(t, path, glmForm(true), "\r\x1b")
	if saved {
		t.Fatal("refused file reported as saved")
	}
	if !strings.Contains(screen, "settings.json is not valid JSON") {
		t.Fatalf("refusal not shown; screen:\n%s", screen)
	}
	if !strings.Contains(screen, "nothing written") {
		t.Fatal("hint does not say nothing was written")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != bad {
		t.Fatal("refused file was modified")
	}
	if strings.Contains(screen, sentinelToken) {
		t.Fatal("token from the refused file reached the screen")
	}
}

func TestEditEscCancelsWithoutWriting(t *testing.T) {
	body := `{"env":{"ANTHROPIC_BASE_URL":"https://glm.example/api"}}`
	path := editFixture(t, body)
	saved, _ := runEdit(t, path, glmForm(false), sentinelToken+"\x1b")
	if saved {
		t.Fatal("esc reported a save")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != body {
		t.Fatal("esc wrote the file")
	}
}

// TestEditPrefillsEndpointButNeverToken: the base URL and model are shown; the
// token is not, and a file that has one says so instead of showing dots.
func TestEditPrefillsEndpointButNeverToken(t *testing.T) {
	out := RenderEdit("glm", "settings.json", "", EditForm{
		BaseURL: "https://glm.example/api", Model: "glm-4.6", HasToken: true,
	}, Size{Cols: 80, Rows: 24})
	for _, want := range []string{"https://glm.example/api", "glm-4.6", "keep current key"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "•") {
		t.Error("untouched token field shows dots: it would look prefilled")
	}
}

// TestEditFrameNeverEchoesToken: even a token the screen holds renders as dots.
func TestEditFrameNeverEchoesToken(t *testing.T) {
	out := RenderEdit("glm", "settings.json", "", EditForm{Token: sentinelToken, HasToken: true}, Size{Cols: 80, Rows: 24})
	if strings.Contains(out, sentinelToken) {
		t.Fatalf("token echoed in frame:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("•", len(sentinelToken))) {
		t.Fatalf("token not masked to its length:\n%s", out)
	}
}

// TestEditFramesFitTerminal is TestFramesFitTerminal for the edit screen: at
// every size, with long values and hints, the frame neither wraps nor scrolls.
func TestEditFramesFitTerminal(t *testing.T) {
	long := "https://" + strings.Repeat("very-long-host-", 8) + "example.com/api/v1"
	for _, sz := range []Size{{20, 10}, {24, 12}, {40, 14}, {60, 8}, {80, 24}, {200, 60}} {
		frames := map[string]string{
			"edit":      RenderEdit("claude-glm-account-with-a-long-name", "settings.json", "", EditForm{Token: strings.Repeat("x", 80), HasToken: true, BaseURL: long, Model: long}, sz),
			"edit-hint": RenderEdit("glm", "settings.json", "settings.json is not valid JSON — nothing written", glmForm(true), sz),
			"edit-tok":  RenderEdit("glm", "settings.json", "", EditForm{Field: 2, BaseURL: long}, sz),
		}
		for name, out := range frames {
			if used := strings.Count(out, "\r\n"); used >= sz.Rows {
				t.Errorf("%s %dx%d: %d rows emitted into a %d-row terminal", name, sz.Cols, sz.Rows, used, sz.Rows)
			}
			for _, ln := range strings.Split(out, "\r\n") {
				if n := visibleLen(ln); n > sz.Cols {
					t.Errorf("%s %dx%d: line is %d wide:\n%q", name, sz.Cols, sz.Rows, n, ln)
					break
				}
			}
		}
	}
}

// TestEditFramesStaySquare: the frame's right edge lines up at a few widths.
func TestEditFramesStaySquare(t *testing.T) {
	for _, cols := range []int{30, 40, 60, 100} {
		out := RenderEdit("glm", "settings.json", "", glmForm(true), Size{Cols: cols, Rows: 24})
		var widths []int
		for _, ln := range strings.Split(out, "\r\n") {
			if strings.Contains(ln, "┃") || strings.Contains(ln, "┏") || strings.Contains(ln, "┗") || strings.Contains(ln, "┣") {
				widths = append(widths, visibleLen(ln))
			}
		}
		for _, w := range widths {
			if w != widths[0] {
				t.Errorf("cols %s: frame rows differ in width: %v", strconv.Itoa(cols), widths)
				break
			}
		}
	}
}

// TestSignalExitCleansInFlightWrite sends a real shutdown signal through the
// signal path while a save is in its write window. The exit seam records the
// code, the save fails, and no temp file holding the token is left behind.
func TestSignalExitCleansInFlightWrite(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://glm.example/api"}}`)
	dir := filepath.Dir(path)
	sigc := make(chan os.Signal, 1)
	exitCode := -1
	exited := make(chan struct{})
	restored := make(chan struct{}, 1)
	old := provider.SettingsWriteWindow
	t.Cleanup(func() { provider.SettingsWriteWindow = old })
	provider.SettingsWriteWindow = func() {
		sigc <- os.Interrupt
		<-exited // the signal path has restored, cleaned up and "exited"
	}
	go exitOnSignal(sigc, func() { restored <- struct{}{} }, func(c int) {
		exitCode = c
		close(exited)
	})

	saved, screen := runEdit(t, path, glmForm(false), sentinelToken+"\r")
	if saved {
		t.Fatal("write reported saved after a signal")
	}
	if exitCode != 130 {
		t.Fatalf("exit code = %d, want 130", exitCode)
	}
	if len(restored) != 1 {
		t.Fatal("terminal was not restored before exit")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".settings-") {
			t.Fatalf("temp file left after signal: %s", e.Name())
		}
	}
	if strings.Contains(screen, sentinelToken) {
		t.Fatal("token reached the screen")
	}
}

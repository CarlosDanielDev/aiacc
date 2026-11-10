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

// readForm is the prefill the picker passes: provider.ReadSettingsEnv's view of
// the file at path.
func readForm(t *testing.T, path string) EditForm {
	t.Helper()
	env, err := provider.ReadSettingsEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	return EditForm{SettingsEnv: env}
}

// glmForm is the prefill for a settings-backed account with base URL and model.
func glmForm(hasToken bool) EditForm {
	return EditForm{SettingsEnv: provider.SettingsEnv{
		Vals: []provider.EnvVal{
			{Key: "ANTHROPIC_BASE_URL", Value: "https://glm.example/api"},
			{Key: "ANTHROPIC_MODEL", Value: "glm-4.6"},
		},
		HasToken: hasToken,
	}}
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
	form := EditForm{SettingsEnv: provider.SettingsEnv{
		Vals:     []provider.EnvVal{{Key: "ANTHROPIC_BASE_URL", Value: "https://old"}},
		HasToken: true,
	}}
	saved, screen := runEdit(t, path, form, keys)
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

// TestEditArrowsMoveFieldsAndNeverCancel: arrows walk the edit screen's fields
// and no sequence cancels it or injects a character.
func TestEditArrowsMoveFieldsAndNeverCancel(t *testing.T) {
	body := `{"env":{"ANTHROPIC_BASE_URL":"https://old"}}`
	// Down to base URL: "x" lands there, appended to the prefill — not in the token.
	path := editFixture(t, body)
	if saved, _ := runEdit(t, path, glmForm(false), "\x1b[Bx\r"); !saved {
		t.Fatal("save not reported")
	}
	if got := fileEnv(t, path)["ANTHROPIC_BASE_URL"]; got != "https://glm.example/apix" {
		t.Fatalf("base URL = %q, want the prefill with x appended", got)
	}
	// Down then up: "x" lands back in the token.
	path = editFixture(t, body)
	if saved, _ := runEdit(t, path, glmForm(false), "\x1b[B\x1b[Ax\r"); !saved {
		t.Fatal("save not reported")
	}
	if got := fileEnv(t, path)["ANTHROPIC_AUTH_TOKEN"]; got != "x" {
		t.Fatalf("token = %q, want x (up moved back to the token)", got)
	}
	// Left/right/Home inject nothing; the base URL keeps only the appended "x".
	path = editFixture(t, body)
	if saved, _ := runEdit(t, path, glmForm(false), "\x1b[B\x1b[C\x1b[D\x1b[Hx\r"); !saved {
		t.Fatal("save not reported")
	}
	if got := fileEnv(t, path)["ANTHROPIC_BASE_URL"]; got != "https://glm.example/apix" {
		t.Fatalf("base URL = %q, want the prefill with only x appended", got)
	}
}

// TestEditListsEveryEnvKeyAndMasksToken: the frame names every env key the file
// holds — model slots included — with its value, the add-key row, and the drop
// affordance; the token appears only as "keep current key", never as dots or a
// value.
func TestEditListsEveryEnvKeyAndMasksToken(t *testing.T) {
	form := EditForm{SettingsEnv: provider.SettingsEnv{
		Vals: []provider.EnvVal{
			{Key: "ANTHROPIC_BASE_URL", Value: "https://ghost.example/api"},
			{Key: "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME", Value: "fast"},
			{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "opus-x"},
		},
		HasToken: true,
	}}
	out := RenderEdit("glm", "ghost.json", "", form, Size{Cols: 80, Rows: 24})
	for _, want := range []string{
		"ANTHROPIC_BASE_URL", "https://ghost.example/api",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME", "fast",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "opus-x",
		"keep current key", "new key", "drop key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("frame misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "•") {
		t.Error("untouched token field shows dots: it would look prefilled")
	}
}

// TestEditRoundTripsModelSlotFile: a file with only a base URL and model-slot
// keys opens, takes an edit on a slot row, and saves exactly that edit — every
// other key and value comes back as it was.
func TestEditRoundTripsModelSlotFile(t *testing.T) {
	body := `{"env":{"ANTHROPIC_BASE_URL":"https://ghost.example","ANTHROPIC_DEFAULT_OPUS_MODEL":"opus-x","ANTHROPIC_DEFAULT_SONNET_MODEL":"sonnet-x","ANTHROPIC_DEFAULT_HAIKU_MODEL":"haiku-x"}}`
	path := editFixture(t, body)
	form := readForm(t, path)
	// Rows: token, BASE_URL, HAIKU, OPUS, SONNET (sorted), add. Three downs put
	// the cursor on OPUS; "x" appends to its value; enter saves.
	saved, screen := runEdit(t, path, form, "\x1b[B\x1b[B\x1b[Bx\r")
	if !saved {
		t.Fatal("save not reported")
	}
	env := fileEnv(t, path)
	if env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "opus-xx" {
		t.Fatalf("opus slot = %q, want opus-xx", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://ghost.example" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "haiku-x" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "sonnet-x" {
		t.Fatalf("untouched keys changed: %v", env)
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("a token was invented")
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL"} {
		if !strings.Contains(screen, key) {
			t.Fatalf("screen never showed %q:\n%s", key, screen)
		}
	}
}

// TestEditAddsKeyAndEditsValue: typing a name on the new-key row and pressing
// enter adds the key and focuses its empty value; typing there and saving
// writes it. Nothing else in the file changes.
func TestEditAddsKeyAndEditsValue(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://x"}}`)
	form := EditForm{SettingsEnv: provider.SettingsEnv{
		Vals: []provider.EnvVal{{Key: "ANTHROPIC_BASE_URL", Value: "https://x"}},
	}}
	// Rows: token, base, new key. Two tabs reach the new-key row.
	keys := "\t\tANTHROPIC_SMALL_FAST_MODEL\rclaude-haiku\r"
	saved, screen := runEdit(t, path, form, keys)
	if !saved {
		t.Fatal("save not reported")
	}
	env := fileEnv(t, path)
	if env["ANTHROPIC_SMALL_FAST_MODEL"] != "claude-haiku" {
		t.Fatalf("added key = %q, want claude-haiku", env["ANTHROPIC_SMALL_FAST_MODEL"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://x" {
		t.Fatalf("existing keys changed: %v", env)
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatal("a model key was invented")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("a token was invented")
	}
	if !strings.Contains(screen, "ANTHROPIC_SMALL_FAST_MODEL") {
		t.Fatalf("new key never shown:\n%s", screen)
	}
}

// TestEditDropsKeyAndKeepsCursorOnAnEnvRow: ctrl-D on a key row removes it, and
// the cursor lands on an env row afterwards — typing then edits a value, never
// a stray new-key name.
func TestEditDropsKeyAndKeepsCursorOnAnEnvRow(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://x","ANTHROPIC_MODEL":"glm-4.6","ANTHROPIC_EXTRA":"gone"}}`)
	form := readForm(t, path)
	// Rows: token, BASE_URL, EXTRA, MODEL (sorted), new key. Three tabs reach
	// MODEL, the last env row; ctrl-D drops it; the cursor must land on the new
	// last env row (EXTRA), where "z" is appended; enter saves. Dropping the
	// last row is the case that needs the clamp: without it the cursor would
	// sit on the new-key row and "z" would become a stray key.
	saved, _ := runEdit(t, path, form, "\t\t\t\x04z\r")
	if !saved {
		t.Fatal("save not reported")
	}
	env := fileEnv(t, path)
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatalf("dropped key survived: %v", env)
	}
	if env["ANTHROPIC_EXTRA"] != "gonez" {
		t.Fatalf("extra = %q, want gonez (cursor stayed on a value row)", env["ANTHROPIC_EXTRA"])
	}
	if env["ANTHROPIC_BASE_URL"] != "https://x" {
		t.Fatalf("base URL changed: %v", env)
	}
	if got, ok := env["z"]; ok {
		t.Fatalf("stray key %q was created by the drop", got)
	}
}

// TestEditAddDuplicateNameJumpsToExistingRow: adding a name the file already
// has does not create a second entry — the cursor jumps to the existing row.
func TestEditAddDuplicateNameJumpsToExistingRow(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://x","ANTHROPIC_FOO":"keep"}}`)
	form := readForm(t, path)
	// Rows: token, BASE_URL, FOO, new key. Three tabs reach the new-key row;
	// typing the existing name and enter jumps to FOO's row; enter saves.
	saved, _ := runEdit(t, path, form, "\t\t\tANTHROPIC_FOO\r\r")
	if !saved {
		t.Fatal("save not reported")
	}
	raw, _ := os.ReadFile(path)
	if n := bytes.Count(raw, []byte(`"ANTHROPIC_FOO"`)); n != 1 {
		t.Fatalf("duplicate FOO entry written (%d occurrences):\n%s", n, raw)
	}
	env := fileEnv(t, path)
	if env["ANTHROPIC_FOO"] != "keep" || env["ANTHROPIC_BASE_URL"] != "https://x" {
		t.Fatalf("values changed by the jump: %v", env)
	}
}

// TestEditAddTokenNameGoesToTokenRow: naming the token key on the new-key row
// lands on the masked token field — the value stays dots on screen and is
// written to the file's token, never as a listed env row.
func TestEditAddTokenNameGoesToTokenRow(t *testing.T) {
	path := editFixture(t, `{"env":{"ANTHROPIC_BASE_URL":"https://x"}}`)
	secret := "plain-secret-value"
	keys := "\t\t\tANTHROPIC_AUTH_TOKEN\r" + secret + "\r"
	saved, screen := runEdit(t, path, glmForm(false), keys)
	if !saved {
		t.Fatal("save not reported")
	}
	env := fileEnv(t, path)
	if env["ANTHROPIC_AUTH_TOKEN"] != secret {
		t.Fatalf("token = %q, want the typed value", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if strings.Contains(screen, secret) {
		t.Fatal("typed token echoed on screen")
	}
}

// TestEditPrefillsEndpointButNeverToken: the base URL and model are shown; the
// token is not, and a file that has one says so instead of showing dots.
func TestEditPrefillsEndpointButNeverToken(t *testing.T) {
	out := RenderEdit("glm", "settings.json", "", glmForm(true), Size{Cols: 80, Rows: 24})
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
	form := EditForm{Token: sentinelToken, SettingsEnv: provider.SettingsEnv{HasToken: true}}
	out := RenderEdit("glm", "settings.json", "", form, Size{Cols: 80, Rows: 24})
	if strings.Contains(out, sentinelToken) {
		t.Fatalf("token echoed in frame:\n%s", out)
	}
	if !strings.Contains(out, strings.Repeat("•", len(sentinelToken))) {
		t.Fatalf("token not masked to its length:\n%s", out)
	}
}

// TestEditFramesFitTerminal is TestFramesFitTerminal for the edit screen: at
// every size, with long values, many env keys and hints, the frame neither
// wraps nor scrolls.
func TestEditFramesFitTerminal(t *testing.T) {
	long := "https://" + strings.Repeat("very-long-host-", 8) + "example.com/api/v1"
	keys := []provider.EnvVal{
		{Key: "ANTHROPIC_BASE_URL", Value: long},
		{Key: "ANTHROPIC_CUSTOM_MODEL_OPTION", Value: long},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "haiku-x"},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "opus-x"},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "sonnet-x"},
		{Key: "ANTHROPIC_SMALL_FAST_MODEL", Value: "a-very-long-model-name-value-indeed"},
	}
	for _, sz := range []Size{{20, 10}, {24, 12}, {40, 14}, {60, 8}, {80, 24}, {200, 60}} {
		frames := map[string]string{
			"edit":      RenderEdit("claude-glm-account-with-a-long-name", "settings.json", "", EditForm{Token: strings.Repeat("x", 80), SettingsEnv: provider.SettingsEnv{Vals: keys, HasToken: true}}, sz),
			"edit-hint": RenderEdit("glm", "settings.json", "settings.json is not valid JSON — nothing written", glmForm(true), sz),
			"edit-tok":  RenderEdit("glm", "settings.json", "", EditForm{Field: 2, SettingsEnv: provider.SettingsEnv{Vals: keys}}, sz),
			"edit-add":  RenderEdit("glm", "settings.json", "", EditForm{Field: len(keys) + 1, AddKey: "ANTHROPIC_NEW", SettingsEnv: provider.SettingsEnv{Vals: keys}}, sz),
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

// TestEditWindowKeepsCursorVisible: with more env keys than fit, the frame
// windows them and always shows the focused row.
func TestEditWindowKeepsCursorVisible(t *testing.T) {
	var vals []provider.EnvVal
	for _, k := range []string{"A_MODEL", "B_MODEL", "C_MODEL", "D_MODEL", "E_MODEL", "F_MODEL"} {
		vals = append(vals, provider.EnvVal{Key: "ANTHROPIC_" + k, Value: "x"})
	}
	focused := "ANTHROPIC_E_MODEL"
	out := RenderEdit("glm", "settings.json", "", EditForm{
		Field:       5,
		SettingsEnv: provider.SettingsEnv{Vals: vals},
	}, Size{Cols: 60, Rows: 18})
	if !strings.Contains(out, focused) {
		t.Fatalf("focused row scrolled out of view:\n%s", out)
	}
	if !strings.Contains(out, "above") && !strings.Contains(out, "below") {
		t.Fatal("windowed list has no more-above/below readout")
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
	t.Cleanup(func() {
		provider.SettingsWriteWindow = old
		provider.ResetSettingsWritesForTest() // the abort latched the process-wide write registry
	})
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

package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// readEnv reads a generated skeleton and returns its env block, failing the
// test when the file is missing or does not parse.
func readEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("skeleton is not valid JSON: %v", err)
	}
	return doc.Env
}

// sample is two launchable claude profiles.
func sample() []Row {
	return []Row{
		{Provider: "claude", Account: "personal", Email: "me@x.com", Dir: "/p", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
		{Provider: "claude", Account: "work", Email: "w@co.com", Dir: "/w", DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
	}
}

func drv(t *testing.T, keys string, rows []Row) Result {
	t.Helper()
	res, err := drive(rows, false, fixedGeom(80, 24), bytes.NewBufferString(keys), &bytes.Buffer{})
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

func TestDriveSetupKey(t *testing.T) {
	if res := drv(t, "s", sample()); res.Kind != Setup {
		t.Fatalf("got %+v, want Setup", res)
	}
}

func TestDriveRenameKey(t *testing.T) {
	// Cursor starts on the first row (0); r renames it.
	if res := drv(t, "r", sample()); res.Kind != Rename || res.Index != 0 {
		t.Fatalf("got %+v, want Rename 0", res)
	}
}

func TestDriveHandoffKey(t *testing.T) {
	if res := drv(t, "h", sample()); res.Kind != Handoff || res.Index != 0 {
		t.Fatalf("got %+v, want Handoff 0", res)
	}
}

// TestDriveResumeKey: `u` resumes a session of the selected row, and the
// legend advertises it.
func TestDriveResumeKey(t *testing.T) {
	if res := drv(t, "ju", sample()); res.Kind != Resume || res.Index != 1 {
		t.Fatalf("got %+v, want Resume 1", res)
	}
	if frame := renderFrame(sample(), 0, false, 0, Size{Cols: 120, Rows: 24}); !strings.Contains(frame, "resume") {
		t.Fatalf("legend does not show u resume:\n%s", frame)
	}
}

// TestLogoGlitchBurst: the static render (phase 0) is clean, and a glitch-phase
// render differs and contains a glitch glyph — without changing the frame width.
func TestLogoGlitchBurst(t *testing.T) {
	rows := sample()
	clean := renderFrame(rows, 0, false, 0, Size{Cols: 58, Rows: 24})
	glitch := renderFrame(rows, 0, false, 13, Size{Cols: 58, Rows: 24}) // 13%14 >= 12 → glitch burst
	if clean == glitch {
		t.Fatal("glitch phase did not alter the frame")
	}
	if !strings.ContainsAny(glitch, string(glitchChars)) {
		t.Fatalf("no glitch glyph in a glitch-phase render:\n%s", glitch)
	}
	// Width invariant still holds under glitch (checked broadly by TestFramesStaySquare;
	// here just ensure the logo rows didn't change rune-width).
	if visibleLen(strings.SplitN(clean, "\r\n", 6)[3]) != visibleLen(strings.SplitN(glitch, "\r\n", 6)[3]) {
		t.Fatal("glitch changed a logo row's visible width")
	}
}

// --- Generic list + message ---------------------------------------------------

func list(t *testing.T, keys string, items []ListItem) int {
	t.Helper()
	i, err := driveList("pick", items, fixedGeom(80, 24), bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveList: %v", err)
	}
	return i
}

func TestDriveListSelectMoveCancel(t *testing.T) {
	items := []ListItem{{Primary: "a"}, {Primary: "b"}, {Primary: "c"}}
	if i := list(t, "\r", items); i != 0 {
		t.Fatalf("enter → %d, want 0", i)
	}
	if i := list(t, "\x1b[B\x1b[B\r", items); i != 2 {
		t.Fatalf("down down enter → %d, want 2", i)
	}
	if i := list(t, "\x1b[B\x1b[A\x1b[A\r", items); i != 0 { // clamps at top
		t.Fatalf("up past top → %d, want 0", i)
	}
	if i := list(t, "q", items); i != -1 {
		t.Fatalf("quit → %d, want -1", i)
	}
}

func TestRenderListShowsItems(t *testing.T) {
	out := RenderList("pick a thing", []ListItem{{Primary: "alpha", Secondary: "detail"}}, 0, Size{Cols: 80, Rows: 24})
	for _, want := range []string{"PICK A THING", "alpha", "detail", ">"} {
		if !strings.Contains(out, want) {
			t.Errorf("list render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMessageShowsBody(t *testing.T) {
	out := RenderMessage("done", []Line{{Text: "all good", Color: Green}}, Size{Cols: 80, Rows: 24})
	for _, want := range []string{"DONE", "all good", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("message render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderShowsSetupNudgeOnlyWhenNeeded(t *testing.T) {
	const nudge = "press "
	if on := Render(sample(), 0, true, Size{Cols: 80, Rows: 24}); !strings.Contains(on, "s") || !strings.Contains(on, "setup") {
		t.Errorf("setup nudge/legend missing when needed:\n%s", on)
	}
	off := Render(sample(), 0, false, Size{Cols: 80, Rows: 24})
	if strings.Contains(off, nudge+"s to install") {
		t.Errorf("setup nudge shown when not needed:\n%s", off)
	}
}

// --- Shell setup --------------------------------------------------------------

func setupResult() SetupResult {
	return SetupResult{
		BinDir: "~/.local/bin", Names: []string{"claude-work", "claude-personal"},
		Example: "claude-work", WorksNow: true,
	}
}

func TestDriveSetupResultExits(t *testing.T) {
	if err := driveSetupResult(setupResult(), fixedGeom(80, 24), bytes.NewBufferString("q"), &bytes.Buffer{}); err != nil {
		t.Fatalf("driveSetupResult: %v", err)
	}
}

func TestRenderSetupResultWorksNow(t *testing.T) {
	out := RenderSetupResult(setupResult(), Size{Cols: 80, Rows: 24})
	for _, want := range []string{"SETUP", "2 command", "~/.local/bin", "claude-work", "claude-personal", "work now"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup result missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSetupResultNamesSkippedLauncher(t *testing.T) {
	r := setupResult()
	r.Skipped = []string{"~/.local/bin/work"}
	out := RenderSetupResult(r, Size{Cols: 80, Rows: 24})
	if !strings.Contains(out, "~/.local/bin/work is not an aiacc launcher") {
		t.Errorf("skipped launcher not named in the result:\n%s", out)
	}
}

func TestRenderSetupResultNeedsNewTerminal(t *testing.T) {
	r := setupResult()
	r.WorksNow = false
	out := RenderSetupResult(r, Size{Cols: 80, Rows: 24})
	if !strings.Contains(out, "new terminal") {
		t.Errorf("expected new-terminal guidance when not on PATH:\n%s", out)
	}
	if strings.Contains(out, "work now") {
		t.Errorf("should not claim works-now when off PATH:\n%s", out)
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
	out := RenderRemove(Row{Provider: "claude", Account: "work"}, Size{Cols: 80, Rows: 24})
	for _, want := range []string{"REMOVE PROFILE", "work", "left on disk", "y", "cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("remove render missing %q:\n%s", want, out)
		}
	}
}

// --- Render -------------------------------------------------------------------

func TestRenderShowsProfilesAndLogin(t *testing.T) {
	out := Render(sample(), 0, false, Size{Cols: 80, Rows: 24})
	for _, want := range []string{">", "work", "w@co.com", "launch a profile", "profiles"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// TestRowSettingsTag: a row whose account carries a settings file shows its
// basename as a dim "⚙ <file>" tag; a row without one shows no gear at all.
func TestRowSettingsTag(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "glm", Email: "g@z.ai", Dir: "/g", DirExists: true,
			EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude", Settings: "/home/u/.glm/glm.json"},
		{Provider: "claude", Account: "plain", Email: "p@x.com", Dir: "/p", DirExists: true,
			EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"},
	}
	out := Render(rows, -1, false, Size{Cols: 80, Rows: 24})
	if want := paint(" ⚙ glm.json", inkDim); !strings.Contains(out, want) {
		t.Errorf("settings tag not rendered dim as %q:\n%s", want, out)
	}
	if got := strings.Count(out, "⚙"); got != 1 {
		t.Errorf("want exactly one gear glyph (the tagged row), got %d:\n%s", got, out)
	}
	if strings.Contains(out, "/home/u") {
		t.Errorf("tag must show the basename only, not the path:\n%s", out)
	}
	// The tag survives selection: the highlighted row shows it too.
	if out := Render(rows, 0, false, Size{Cols: 80, Rows: 24}); !strings.Contains(out, "⚙ glm.json") {
		t.Errorf("focused row lost the settings tag:\n%s", out)
	}
}

func TestRenderBlockedBadges(t *testing.T) {
	rows := []Row{
		{Provider: "claude", Account: "gone", DirExists: false, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"}, // dir missing
		{Provider: "openai", Account: "x", DirExists: true, EnvVar: "OPENAI_CONFIG", Command: ""},               // no launcher
	}
	out := Render(rows, -1, false, Size{Cols: 80, Rows: 24})
	for _, want := range []string{"dir missing", "no launcher"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing blocked badge %q:\n%s", want, out)
		}
	}
}

func TestRenderEmptyState(t *testing.T) {
	out := Render(nil, -1, false, Size{Cols: 80, Rows: 24})
	plain := ansi.ReplaceAllString(out, "")
	if !strings.Contains(plain, "No profiles yet") {
		t.Errorf("empty state missing guidance:\n%s", out)
	}
	// The empty state points at the endpoint flow, not just the add key.
	if !strings.Contains(plain, "settings: glm for an API key") {
		t.Errorf("empty state does not name the endpoint keyword:\n%s", out)
	}
}

// --- Add profile --------------------------------------------------------------

func add(t *testing.T, keys string) AddResult {
	t.Helper()
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	return res
}

func TestAddTypeNameEnter(t *testing.T) {
	res := add(t, "claude-work\r")
	if !res.OK || res.Name != "claude-work" || res.Dir != "~/.claude-work" {
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

func TestAddProviderSetsDirDefault(t *testing.T) {
	// The default dir follows the chosen provider, not always claude.
	res, err := driveAdd("", "codex", false, fixedGeom(80, 24), bytes.NewBufferString("work\r"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if !res.OK || res.Dir != "~/.codex-work" {
		t.Fatalf("got %+v, want dir=~/.codex-work", res)
	}
}

// The settings field is a third stop for a provider that supports it; the path
// is taken verbatim, like the dir.
func TestAddSettingsViaSecondTab(t *testing.T) {
	res := add(t, "glm\t\t~/.claude-glm/glm.json\r")
	if !res.OK || res.Name != "glm" || res.Dir != "~/.claude-glm" || res.Settings != "~/.claude-glm/glm.json" {
		t.Fatalf("got %+v, want the settings path", res)
	}
}

// Without a settings flag, ⇥ cycles name ↔ dir only, so a third stop can never
// collect a path the launcher would drop.
func TestAddWithoutSettingsCyclesTwoFields(t *testing.T) {
	res, err := driveAdd("", "codex", false, fixedGeom(80, 24), bytes.NewBufferString("w\t\tx\r"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if !res.OK || res.Name != "wx" || res.Settings != "" {
		t.Fatalf("got %+v, want name=wx and no settings", res)
	}
}

// An endpoint keyword in the settings field generates the skeleton next to the
// profile dir and registers the generated path, exactly as if the user had
// written the file by hand and passed its path.
func TestAddSettingsKeyword(t *testing.T) {
	dir := t.TempDir()
	out := &bytes.Buffer{}
	keys := "w\t" + dir + "\tglm\r"
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	want := filepath.Join(dir, "glm.json")
	if !res.OK || res.Settings != want {
		t.Fatalf("got %+v, want settings=%s (the generated path)", res, want)
	}
	env := readEnv(t, want)
	if env["ANTHROPIC_BASE_URL"] != provider.Presets["claude"].Endpoints["glm"].BaseURL || env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Fatalf("env = %v, want the glm endpoint skeleton", env)
	}
}

// A keyword whose target file already exists aborts the create: nothing is
// overwritten, nothing is registered, and the screen names the existing path.
func TestAddSettingsExisting(t *testing.T) {
	// A short dir so the whole naming sentence fits the hint row's width
	// ceiling and the assertion can pin it in full.
	dir, err := os.MkdirTemp("/tmp", "")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	existing := filepath.Join(dir, "glm.json")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out := &bytes.Buffer{}
	keys := "w\t" + dir + "\tglm\r\x1b"
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if res.OK {
		t.Fatalf("got %+v, want the create aborted", res)
	}
	if raw, err := os.ReadFile(existing); err != nil || string(raw) != "keep\n" {
		t.Fatalf("existing file touched: %q, %v", raw, err)
	}
	if !strings.Contains(ansi.ReplaceAllString(out.String(), ""), "glm.json already exists in "+dir) {
		t.Errorf("screen does not name %s:\n%s", existing, out)
	}
}

// TestAddSettingsExistingLongDir pins the refusal against the canonical flow's
// long dir: the hint row truncates at the inner width minus 4 columns (the
// padding and the ⚠ marker), so the file name leads the message and its glyph
// must survive any real width.
func TestAddSettingsExistingLongDir(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude-somelongprofilename"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	existing := filepath.Join(home, ".claude-somelongprofilename", "glm.json")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out := &bytes.Buffer{}
	keys := "w\t~/.claude-somelongprofilename\tglm\r\x1b"
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if res.OK {
		t.Fatalf("got %+v, want the create aborted", res)
	}
	if raw, err := os.ReadFile(existing); err != nil || string(raw) != "keep\n" {
		t.Fatalf("existing file touched: %q, %v", raw, err)
	}
	if !strings.Contains(ansi.ReplaceAllString(out.String(), ""), "glm.json already exists") {
		t.Errorf("hint does not name the existing file at a long dir:\n%s", out)
	}
}

// A real path in the settings field keeps today's verbatim behavior — no
// generation is attempted for it — while the custom keyword is accepted and
// generates a skeleton whose empty base URL the user fills in the file.
func TestAddSettingsPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.json")
	keys := "w\t" + dir + "\t" + path + "\r"
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if !res.OK || res.Settings != path {
		t.Fatalf("got %+v, want the path verbatim", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a real path must not be created by the add screen: %v", err)
	}

	out := &bytes.Buffer{}
	keys = "w\t" + dir + "\tcustom\r"
	res, err = driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
	if err != nil {
		t.Fatalf("driveAdd custom: %v", err)
	}
	want := filepath.Join(dir, "custom.json")
	if !res.OK || res.Settings != want {
		t.Fatalf("got %+v, want settings=%s for the custom keyword", res, want)
	}
	env := readEnv(t, want)
	if base, ok := env["ANTHROPIC_BASE_URL"]; !ok || base != "" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q (present=%t), want present but empty", base, ok)
	}
}

// lastFrame is the screen the user is left looking at: the text after the
// last cursor-home repaint, with escape codes stripped.
func lastFrame(out string) string {
	frames := strings.Split(out, "\x1b[H")
	return ansi.ReplaceAllString(frames[len(frames)-1], "")
}

// hintLine is the rendered hint row (the one carrying the ⚠ marker). The
// settings field echoes the typo on its own row, so assertions about the hint
// must run on this line alone, never on the whole frame.
func hintLine(frame string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "⚠") {
			return l
		}
	}
	return ""
}

// hintText is the refusal as the hint rows show it: the ⚠ row and each
// continuation row under it, up to the blank row or the box bottom, their
// inner text joined by single spaces.
func hintText(frame string) string {
	var words []string
	started := false
	for _, l := range strings.Split(frame, "\n") {
		_, inner, framed := strings.Cut(l, "┃")
		inner, _, _ = strings.Cut(inner, "┃")
		if !started {
			if !framed || !strings.Contains(inner, "⚠") {
				continue
			}
			started, inner = true, strings.Replace(inner, "⚠", "", 1)
		}
		if !framed || strings.TrimSpace(inner) == "" {
			break
		}
		words = append(words, strings.Fields(inner)...)
	}
	return strings.Join(words, " ")
}

// typoRefusal is the refusal text the add screen would show for a typo.
func typoRefusal(t *testing.T, typo string) string {
	t.Helper()
	_, keyword, err := provider.ClassifySettings("claude", typo)
	if err == nil || keyword {
		t.Fatalf("ClassifySettings(%q) = keyword %t, err %v; want a refusal", typo, keyword, err)
	}
	return err.Error()
}

// A bare word that names no keyword is refused with the valid ones listed —
// the same gate the CLI's --settings flag applies. The refusal stays in the
// hint row of the last frame, leads with the keywords so a long typo cannot
// truncate them off the row, and leaves nothing written to the dir.
func TestAddSettingsKeywordTypo(t *testing.T) {
	for _, typo := range []string{"kimi", "kimi-coding-plan-v2-enterprise-tier-x"} {
		t.Run(typo, func(t *testing.T) {
			dir := t.TempDir()
			out := &bytes.Buffer{}
			keys := "w\t" + dir + "\t" + typo + "\r\x1b"
			res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
			if err != nil {
				t.Fatalf("driveAdd: %v", err)
			}
			if res.OK {
				t.Fatalf("got %+v, want the create aborted for the typo", res)
			}
			hint := hintLine(lastFrame(out.String()))
			if hint == "" {
				t.Fatalf("no hint row on the last frame:\n%s", lastFrame(out.String()))
			}
			if !strings.Contains(hint, "keywords: custom, glm") {
				t.Errorf("hint row does not name the valid keywords up front: %q", hint)
			}
			// The whole refusal is on screen, wrapped onto as many hint rows as
			// it needs: never cut with "…", the typo always named.
			if got, want := hintText(lastFrame(out.String())), strings.Join(strings.Fields(typoRefusal(t, typo)), " "); got != want {
				t.Errorf("hint rows show %q, want the whole refusal %q", got, want)
			}
			if strings.Contains(lastFrame(out.String()), "…") {
				t.Errorf("refusal cut with …:\n%s", lastFrame(out.String()))
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a typo wrote %d file(s) into the dir", len(entries))
			}
		})
	}
}

// At any width, down to innerWidth's floor and below it, the hint rows carry
// the whole refusal: a word wider than a row is split across rows, never cut
// with "…". Compared with spaces removed, since a split word gains a row break.
func TestAddSettingsKeywordsAtNarrowWidth(t *testing.T) {
	squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
	for _, cols := range []int{32, 28, 27} {
		for _, typo := range []string{"kimi", "kimi-coding-plan-v2-enterprise-tier-x"} {
			t.Run(fmt.Sprintf("%dcols/%s", cols, typo), func(t *testing.T) {
				out := &bytes.Buffer{}
				keys := "w\t\t" + typo + "\r\x1b"
				if _, err := driveAdd("", "claude", true, fixedGeom(cols, 24), bytes.NewBufferString(keys), out); err != nil {
					t.Fatalf("driveAdd: %v", err)
				}
				frame := lastFrame(out.String())
				if got, want := squash(hintText(frame)), squash(typoRefusal(t, typo)); got != want {
					t.Errorf("at %d columns the hint rows show %q, want %q", cols, got, want)
				}
				if strings.Contains(frame, "…") {
					t.Errorf("at %d columns the refusal is cut with …:\n%s", cols, frame)
				}
			})
		}
	}
}

func TestRenderAddSettingsField(t *testing.T) {
	sz := Size{Cols: 80, Rows: 24}
	out := RenderAdd("", "claude", AddForm{Name: "glm", WithSettings: true}, "", sz)
	if !strings.Contains(out, "settings") {
		t.Errorf("settings field missing:\n%s", out)
	}
	// The placeholder names the keywords, so they can be discovered.
	if !strings.Contains(out, "glm · custom — or a path") {
		t.Errorf("settings placeholder does not name the keywords:\n%s", out)
	}
	if out := RenderAdd("", "codex", AddForm{Name: "w"}, "", sz); strings.Contains(out, "settings") {
		t.Errorf("settings field shown for a provider without one:\n%s", out)
	}
}

func TestRenderAddShowsFieldsAndLogin(t *testing.T) {
	out := RenderAdd("me@x.com", "claude", AddForm{Name: "wo"}, "", Size{Cols: 80, Rows: 24})
	for _, want := range []string{"ADD PROFILE", "provider", "claude", "name", "wo", "~/.claude-wo", "me@x.com", "launches as: wo"} {
		if !strings.Contains(out, want) {
			t.Errorf("add render missing %q:\n%s", want, out)
		}
	}
}

// --- Rename profile -----------------------------------------------------------

func rename(t *testing.T, old, keys string, taken map[string]bool) string {
	t.Helper()
	out, err := driveRename(old, taken, fixedGeom(80, 24), bytes.NewBufferString(keys), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("driveRename: %v", err)
	}
	return out
}

func TestRenameEditAndConfirm(t *testing.T) {
	// Backspace "work" to "wo", type "-x" → "wo-x", enter.
	if got := rename(t, "work", "\x7f\x7f-x\r", nil); got != "wo-x" {
		t.Fatalf("got %q, want wo-x", got)
	}
}

func TestRenameUnchangedIsNoOp(t *testing.T) {
	if got := rename(t, "work", "\r", nil); got != "" {
		t.Fatalf("got %q, want \"\" (unchanged → no-op)", got)
	}
}

func TestRenameEscCancels(t *testing.T) {
	if got := rename(t, "work", "zzz\x1b", nil); got != "" {
		t.Fatalf("got %q, want cancelled", got)
	}
}

// TestRenameBlocksCollision: enter is refused while the new name is already
// taken; after changing to a free name it succeeds.
func TestRenameBlocksCollision(t *testing.T) {
	// "work" -> backspace all, type "personal" (taken) → enter blocked → append
	// "2" → "personal2" (free) → enter.
	if got := rename(t, "work", "\x7f\x7f\x7f\x7fpersonal\r2\r", map[string]bool{"personal": true}); got != "personal2" {
		t.Fatalf("got %q, want personal2", got)
	}
}

func TestRenameRejectsJunk(t *testing.T) {
	// "work" -> backspace all, type "ok?!x" (junk filtered → "okx"), enter.
	if got := rename(t, "work", "\x7f\x7f\x7f\x7fok?!x\r", nil); got != "okx" {
		t.Fatalf("got %q, want okx", got)
	}
}

func TestRenderRenameShowsNames(t *testing.T) {
	out := RenderRename("work", "wo", "", Size{Cols: 80, Rows: 24})
	for _, want := range []string{"RENAME PROFILE", "from", "work", "the command becomes: wo"} {
		if !strings.Contains(out, want) {
			t.Errorf("rename render missing %q:\n%s", want, out)
		}
	}
}

// --- Layout self-test ---------------------------------------------------------

// strip every CSI escape (colour m-codes plus cursor/erase H, K, J).
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

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
		frames["picker"] = Render(rows, 0, true, Size{Cols: cols, Rows: 24})
		frames["add"] = RenderAdd("me@example.com", "claude", AddForm{Name: "work", WithSettings: true}, "bad name", Size{Cols: cols, Rows: 24})
		frames["remove"] = RenderRemove(Row{Provider: "claude", Account: "work"}, Size{Cols: cols, Rows: 24})
		frames["rename"] = RenderRename("work", "work-2", "", Size{Cols: cols, Rows: 24})
		frames["setup"] = RenderSetupResult(setupResult(), Size{Cols: cols, Rows: 24})
		for name, out := range frames {
			wid := -1
			for _, ln := range strings.Split(out, "\r\n") {
				trimmed := strings.TrimLeft(ansi.ReplaceAllString(ln, ""), " ")
				if !strings.ContainsAny(firstRune(trimmed), "┏┃┗┣") {
					continue
				}
				if n := visibleLen(ln); wid == -1 {
					wid = n
				} else if n != wid {
					t.Fatalf("%s cols=%d: framed line width %d != %d:\n%q", name, cols, n, wid, ln)
				}
			}
			if wid == -1 {
				t.Fatalf("%s cols=%d: no framed lines found", name, Size{Cols: cols, Rows: 24})
			}
		}
	}
}

// TestFramesFitTerminal is the poka-yoke that TestFramesStaySquare can't be: a
// frame may be perfectly square and still be wider or taller than the terminal
// showing it — at which point every line wraps, each following row lands low,
// and the box tears. Nothing a screen renders may exceed the screen.
func TestFramesFitTerminal(t *testing.T) {
	var many []Row
	for i := range 40 {
		many = append(many, Row{
			Provider: "claude", Account: "profile-" + strconv.Itoa(i),
			Email: "someone.with.a.long.address@example.com", DirExists: true,
			EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude",
		})
	}
	items := make([]ListItem, 40)
	for i := range items {
		items[i] = ListItem{Primary: "session-" + strconv.Itoa(i), Secondary: "2026-01-01 · a long title"}
	}
	body := make([]Line, 4)
	for i := range body {
		body[i] = Line{Text: "a message line that is quite long indeed", Color: White}
	}

	for _, sz := range []Size{{20, 10}, {24, 12}, {40, 14}, {60, 8}, {80, 24}, {200, 60}} {
		frames := map[string]string{
			"picker":         Render(many, 20, true, sz),
			"list":           RenderList("pick a session", items, 20, sz),
			"remove":         RenderRemove(many[0], sz),
			"add":            RenderAdd("me@example.com", "claude", AddForm{Name: "work", WithSettings: true}, "bad name", sz),
			"rename":         RenderRename("work", "work-2", "", sz),
			"setup":          RenderSetupResult(setupResult(), sz),
			"msg":            RenderMessage("aiacc — shared assets", body, sz),
			"done-unchecked": renderCheckMessage("hand off · done", checkBody(false), Line{}, &testBox, false, sz),
			"done-checked":   renderCheckMessage("hand off · done", checkBody(true), Line{}, &testBox, true, sz),
		}
		for name, out := range frames {
			// One newline per emitted row: the cursor must never be pushed past
			// the last row, which would scroll the screen out from under the
			// cursor-home repaint.
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

// TestPickerWindowsLongList: a list taller than the terminal scrolls inside the
// frame, keeping the cursor visible and saying how much is out of view.
func TestPickerWindowsLongList(t *testing.T) {
	var rows []Row
	for i := range 30 {
		rows = append(rows, Row{Provider: "claude", Account: "p" + strconv.Itoa(i),
			DirExists: true, EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude"})
	}
	out := Render(rows, 25, false, Size{Cols: 80, Rows: 24})
	if !strings.Contains(out, "p25") {
		t.Fatal("cursor row scrolled out of view")
	}
	if !strings.Contains(out, "above") || !strings.Contains(out, "below") {
		t.Fatalf("no out-of-view readout:\n%s", out)
	}
	if strings.Contains(out, "p0 ") {
		t.Fatal("the whole list was rendered instead of a window")
	}
}

// TestGeomTracksResize: a repaint after a resize starts with a full erase, once.
func TestGeomTracksResize(t *testing.T) {
	g := fixedGeom(80, 24)
	if repaint(g) != "" {
		t.Fatal("a steady terminal should repaint in place")
	}
	g.set(Size{Cols: 40, Rows: 12})
	if g.get() != (Size{Cols: 40, Rows: 12}) {
		t.Fatalf("resize not picked up: %+v", g.get())
	}
	if repaint(g) != clearHome {
		t.Fatal("a resized terminal must be erased once")
	}
	if repaint(g) != "" {
		t.Fatal("the erase must not repeat on every later frame")
	}
}

// --- Ctrl-C copy ---------------------------------------------------------------

// TestCopyTextPrefersCommands: a hand-off screen exists to give you a command to
// run, so Ctrl-C copies the commands — not the prose around them.
func TestCopyTextPrefersCommands(t *testing.T) {
	body := []Line{
		{Text: "✓ Copied to claude-work", Color: Green},
		{},
		{Text: "Resume it:", Color: White},
		{Text: "cd /Users/carlos/kyte/ai", Color: Blue},
		{Text: "claude-work --resume a2896e1e", Color: Blue},
	}
	want := "cd /Users/carlos/kyte/ai\nclaude-work --resume a2896e1e"
	if got := copyText(body); got != want {
		t.Fatalf("copyText = %q, want %q", got, want)
	}
}

// TestCopyTextFallsBackToBody: a screen with no command lines still copies
// something, so the key is never inert.
func TestCopyTextFallsBackToBody(t *testing.T) {
	body := []Line{{Text: "No sessions found for this account yet.", Color: Grey}, {}}
	if got := copyText(body); got != "No sessions found for this account yet." {
		t.Fatalf("copyText = %q", got)
	}
	if got := copyText(nil); got != "" {
		t.Fatalf("empty body should copy nothing, got %q", got)
	}
}

// TestCtrlCIsItsOwnKey: Ctrl-C arrives as a byte under -isig. It is decoded
// separately so a screen with something to copy can take it.
func TestCtrlCIsItsOwnKey(t *testing.T) {
	k, err := readKey(bufio.NewReader(bytes.NewBufferString("\x03")))
	if err != nil || k != keyCopy {
		t.Fatalf("readKey(Ctrl-C) = %v, %v; want keyCopy", k, err)
	}
}

// TestPickerStillQuitsOnCtrlC: the picker has nothing to copy, so Ctrl-C must
// keep meaning "get me out of here".
func TestPickerStillQuitsOnCtrlC(t *testing.T) {
	if res := drv(t, "\x03", sample()); res.Kind != Cancelled {
		t.Fatalf("got %+v, want Cancelled", res)
	}
	// …and it also backs out of the remove confirmation, like `n`.
	if res := drv(t, "d\x03q", sample()); res.Kind != Cancelled {
		t.Fatalf("Ctrl-C at the confirm prompt: got %+v, want Cancelled", res)
	}
}

// TestMessageOffersCopyOnlyWhenThereIsSomething: the legend advertises ^C only
// on a screen that has something to put on the clipboard.
func TestMessageOffersCopyOnlyWhenThereIsSomething(t *testing.T) {
	sz := Size{Cols: 80, Rows: 24}
	with := RenderMessage("hand off · done", []Line{{Text: "claude-work --resume x", Color: Blue}}, sz)
	if !strings.Contains(with, "^C") {
		t.Fatalf("no copy hint on a screen with a command:\n%s", with)
	}
	without := RenderMessage("hand off", nil, sz)
	if strings.Contains(without, "^C") {
		t.Fatalf("copy hint on a screen with nothing to copy:\n%s", without)
	}
}

// TestMessageNoteRendersUnderTheBody: the copy confirmation is shown in-screen.
func TestMessageNoteRendersUnderTheBody(t *testing.T) {
	out := renderMessage("hand off · done",
		[]Line{{Text: "claude-work --resume x", Color: Blue}},
		Line{Text: "✓ copied to clipboard", Color: Green}, Size{Cols: 80, Rows: 24})
	if !strings.Contains(out, "copied to clipboard") {
		t.Fatalf("note missing:\n%s", out)
	}
}

// The add screen refuses a settings value that names a directory, keeps the
// screen open, and says why in the hint row.
func TestAddSettingsRefusesDirectory(t *testing.T) {
	settingsDir := filepath.Join(t.TempDir(), "claude-glm")
	if err := os.Mkdir(settingsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out := &bytes.Buffer{}
	keys := "w\t" + t.TempDir() + "\t" + settingsDir + "\r\x1b"
	res, err := driveAdd("", "claude", true, fixedGeom(80, 24), bytes.NewBufferString(keys), out)
	if err != nil {
		t.Fatalf("driveAdd: %v", err)
	}
	if res.OK {
		t.Fatalf("got %+v, want a directory refused", res)
	}
	if !strings.Contains(ansi.ReplaceAllString(out.String(), ""), "settings path is a directory") {
		t.Errorf("screen does not name the refusal:\n%s", out)
	}
}

// checkBody is a toggling message body: its resume line carries the flag only
// when the box is checked, which is what the hand-off done screen does.
func checkBody(checked bool) []Line {
	cmd := "work --resume abc123"
	if checked {
		cmd = "work --dangerously-skip-permissions --resume abc123"
	}
	return []Line{{Text: "✓ Copied to work", Color: Green}, {}, {Text: cmd, Color: Blue}}
}

var testBox = Checkbox{Label: "bypass permissions (--dangerously-skip-permissions)", Warning: "the agent will run tools without asking"}

// runCheck drives a checkbox message with keys, returning the final frame, the
// text handed to the clipboard (or "" if none) and any error.
func runCheck(t *testing.T, keys string) (frame, copied string) {
	t.Helper()
	out := &bytes.Buffer{}
	err := driveMessage("hand off · done", checkBody, &testBox, fixedGeom(80, 24),
		bytes.NewBufferString(keys), out, func(s string) bool { copied = s; return true })
	if err != nil {
		t.Fatalf("driveMessage: %v", err)
	}
	return lastFrame(out.String()), copied
}

// TestCheckboxDefaultsOff: the box opens unchecked and the resume line is the
// plain one, without the flag.
func TestCheckboxDefaultsOff(t *testing.T) {
	frame, _ := runCheck(t, "q")
	if !strings.Contains(frame, "[ ] bypass permissions (--dangerously-skip-permissions)") {
		t.Fatalf("unchecked box missing:\n%s", frame)
	}
	if strings.Contains(frame, "--dangerously-skip-permissions --resume") || strings.Contains(frame, "will run tools") {
		t.Fatalf("flag or warning shown while unchecked:\n%s", frame)
	}
}

// TestCheckboxSpaceAndBToggle: space and b both flip the box, and the frame
// then shows the flag in the resume line and the warning under the box.
func TestCheckboxSpaceAndBToggle(t *testing.T) {
	on, _ := runCheck(t, " q")
	if !strings.Contains(on, "[x] bypass permissions") || !strings.Contains(on, "work --dangerously-skip-permissions --resume abc123") {
		t.Fatalf("space did not check the box:\n%s", on)
	}
	if !strings.Contains(on, "the agent will run tools without asking") {
		t.Fatalf("no warning line when checked:\n%s", on)
	}
	if b, _ := runCheck(t, "bq"); !strings.Contains(b, "[x] bypass permissions") {
		t.Fatalf("b did not check the box:\n%s", b)
	}
	off, _ := runCheck(t, " bq")
	if !strings.Contains(off, "[ ] bypass permissions") || strings.Contains(off, "--dangerously-skip-permissions --resume") {
		t.Fatalf("second toggle did not uncheck:\n%s", off)
	}
}

// TestCheckboxCopyMatchesShown: ^C copies exactly the command lines the frame
// shows, so the flag is in the clipboard iff it is on the screen.
func TestCheckboxCopyMatchesShown(t *testing.T) {
	frame, copied := runCheck(t, " \x03q")
	want := "work --dangerously-skip-permissions --resume abc123"
	if copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	if !strings.Contains(frame, want) {
		t.Fatalf("copied line is not the one shown:\n%s", frame)
	}
	_, plain := runCheck(t, "\x03q")
	if plain != "work --resume abc123" {
		t.Fatalf("unchecked copy = %q", plain)
	}
}

// TestCheckboxLegendShowsKey: the legend names the toggle and says ^C copies.
func TestCheckboxLegendShowsKey(t *testing.T) {
	frame, _ := runCheck(t, "q")
	if !strings.Contains(frame, "space/b") || !strings.Contains(frame, "bypass") {
		t.Fatalf("legend does not show the toggle key:\n%s", frame)
	}
}

// TestPlainMessageIgnoresToggleKeys: with no checkbox, space and b draw no box
// and leave the body in its initial state, even when the body reads the flag.
func TestPlainMessageIgnoresToggleKeys(t *testing.T) {
	out := &bytes.Buffer{}
	body := func(checked bool) []Line {
		if checked {
			return []Line{{Text: "CHECKED-STATE", Color: Blue}}
		}
		return []Line{{Text: "initial-state", Color: Blue}}
	}
	err := driveMessage("hand off · done", body, nil, fixedGeom(80, 24),
		bytes.NewBufferString(" bq"), out, func(string) bool { return true })
	if err != nil {
		t.Fatalf("driveMessage: %v", err)
	}
	frame := lastFrame(out.String())
	if strings.Contains(frame, "[ ]") || strings.Contains(frame, "space/b") {
		t.Fatalf("plain message grew a checkbox:\n%s", frame)
	}
	if strings.Contains(out.String(), "CHECKED-STATE") {
		t.Fatalf("space or b changed the state of a plain message:\n%s", out.String())
	}
}

// --- Duplicate profile --------------------------------------------------------

func TestDriveDuplicateKey(t *testing.T) {
	if res := drv(t, "c", sample()); res.Kind != Duplicate || res.Index != 0 {
		t.Fatalf("got %+v, want Duplicate 0", res)
	}
	if out := Render(sample(), 0, false, Size{Cols: 120, Rows: 24}); !strings.Contains(ansi.ReplaceAllString(out, ""), "c duplicate") {
		t.Fatalf("legend does not offer c duplicate:\n%s", out)
	}
}

// duplicate drives the duplicate screen with keys; submit records each answer
// and refuses the ones in refuse, the way the CLI's function would.
func duplicate(t *testing.T, keys string, refuse map[string]string) (ok bool, calls [][2]string, out string) {
	t.Helper()
	buf := &bytes.Buffer{}
	ok, err := driveDuplicate("claude", "work", func(name, dir string) error {
		calls = append(calls, [2]string{name, dir})
		if msg, bad := refuse[name]; bad {
			return fmt.Errorf("%s", msg)
		}
		return nil
	}, fixedGeom(80, 24), bytes.NewBufferString(keys), buf)
	if err != nil {
		t.Fatalf("driveDuplicate: %v", err)
	}
	return ok, calls, ansi.ReplaceAllString(buf.String(), "")
}

// Enter submits the typed name and dir; an empty dir is passed as empty, so the
// one default rule (the CLI's) applies.
func TestDuplicateSubmitsNameAndDir(t *testing.T) {
	ok, calls, _ := duplicate(t, "w2\r", nil)
	if !ok || len(calls) != 1 || calls[0] != [2]string{"w2", ""} {
		t.Fatalf("ok=%v calls=%v, want one submit of (w2, \"\")", ok, calls)
	}
	ok, calls, _ = duplicate(t, "w2\t~/x\r", nil)
	if !ok || len(calls) != 1 || calls[0] != [2]string{"w2", "~/x"} {
		t.Fatalf("ok=%v calls=%v, want one submit of (w2, ~/x)", ok, calls)
	}
}

// A refusal stays on the screen's hint row and the screen stays open; Esc then
// leaves with nothing done. Esc alone never submits.
func TestDuplicateRefusalShownThenEsc(t *testing.T) {
	ok, calls, out := duplicate(t, "taken\r\x1b", map[string]string{"taken": "taken is in use"})
	if ok || len(calls) != 1 {
		t.Fatalf("ok=%v calls=%v, want the one refused submit and no success", ok, calls)
	}
	if !strings.Contains(out, "taken is in use") {
		t.Fatalf("refusal not shown on screen:\n%s", out)
	}
	if ok, calls, _ := duplicate(t, "w2\x1b", nil); ok || len(calls) != 0 {
		t.Fatalf("Esc: ok=%v calls=%v, want nothing submitted", ok, calls)
	}
}

// The duplicate screen's dir placeholder is the shared default: a name that
// already carries the provider prefix is not prefixed twice.
func TestRenderDuplicateDefaultDir(t *testing.T) {
	out := ansi.ReplaceAllString(RenderDuplicate("claude", "claude-glm", AddForm{Name: "claude-glm2"}, "", Size{Cols: 80, Rows: 24}), "")
	if !strings.Contains(out, "~/.claude-glm2") || strings.Contains(out, "claude-claude") {
		t.Fatalf("dir placeholder is not ~/.claude-glm2:\n%s", out)
	}
}

// The real refusal for a leftover dir is wider than the hint row at 80
// columns: it wraps onto a second row, so the --dir remedy stays visible.
func TestDuplicateRefusalWrapsAt80(t *testing.T) {
	const refusal = "~/.claude-claude-glm2 is not empty: pick another dir (--dir)"
	if len(refusal) <= innerWidth(80)-4 {
		t.Fatalf("refusal fits one row at 80 columns; the test no longer exercises wrapping")
	}
	_, _, out := duplicate(t, "taken\r\x1b", map[string]string{"taken": refusal})
	frame := lastFrame(out)
	if got := hintText(frame); got != refusal {
		t.Fatalf("hint rows show %q, want %q:\n%s", got, refusal, frame)
	}
	if strings.Contains(frame, "…") {
		t.Fatalf("refusal cut with …:\n%s", frame)
	}
}

func TestDuplicateFramesFit(t *testing.T) {
	for _, sz := range []Size{{20, 10}, {40, 14}, {80, 24}, {200, 60}} {
		out := RenderDuplicate("claude", "work", AddForm{Name: "work2"}, "a long refusal that will not fit a narrow frame", sz)
		wid := -1
		for _, ln := range strings.Split(out, "\r\n") {
			if n := visibleLen(ln); n > sz.Cols {
				t.Fatalf("%v: line is %d wide:\n%q", sz, n, ln)
			}
			trimmed := strings.TrimLeft(ansi.ReplaceAllString(ln, ""), " ")
			if !strings.ContainsAny(firstRune(trimmed), "┏┃┗┣") {
				continue
			}
			if n := visibleLen(ln); wid == -1 {
				wid = n
			} else if n != wid {
				t.Fatalf("%v: framed line width %d != %d", sz, n, wid)
			}
		}
		if used := strings.Count(out, "\r\n"); used >= sz.Rows {
			t.Fatalf("%v: %d rows emitted", sz, used)
		}
	}
}

// TestDriveListToggle: on a list with a toggle, b and space flip it, the title
// and legend show its state, and ⏎ returns it with the chosen row. It starts off.
func TestDriveListToggle(t *testing.T) {
	items := []ListItem{{Primary: "a"}, {Primary: "b"}}
	for _, tc := range []struct {
		keys   string
		row    int
		on     bool
		screen string
	}{
		{"\r", 0, false, "b bypass: off"},
		{"b\x1b[B\r", 1, true, "b bypass: on"},
		{"b \r", 0, false, "b bypass: off"},
	} {
		var out bytes.Buffer
		i, on, err := driveListToggle("pick", items, "bypass", fixedGeom(80, 24), bytes.NewBufferString(tc.keys), &out)
		if err != nil || i != tc.row || on != tc.on {
			t.Fatalf("keys %q: got (%d, %v, %v), want (%d, %v)", tc.keys, i, on, err, tc.row, tc.on)
		}
		last := ansi.ReplaceAllString(out.String(), "")
		if j := strings.LastIndex(last, "PICK"); j >= 0 {
			last = last[j:]
		}
		if !strings.Contains(last, tc.screen) {
			t.Fatalf("keys %q: last screen lacks %q:\n%s", tc.keys, tc.screen, last)
		}
		if strings.Contains(last, "· BYPASS ON") != tc.on {
			t.Fatalf("keys %q: title bypass state wrong:\n%s", tc.keys, last)
		}
	}
	if _, on, _ := driveListToggle("pick", items, "bypass", fixedGeom(80, 24), bytes.NewBufferString("bq"), &bytes.Buffer{}); on {
		t.Fatal("cancel must not report the toggle on")
	}
}

// TestRenderListWithoutToggleUnchanged: a list without a toggle renders exactly
// what RenderList always has — no toggle in its legend.
func TestRenderListWithoutToggleUnchanged(t *testing.T) {
	sz := Size{Cols: 80, Rows: 24}
	items := []ListItem{{Primary: "alpha", Secondary: "detail"}}
	if got, want := renderList("pick", items, 0, "", false, sz), RenderList("pick", items, 0, sz); got != want {
		t.Fatalf("renderList with no toggle differs from RenderList")
	}
	if strings.Contains(ansi.ReplaceAllString(RenderList("pick", items, 0, sz), ""), "bypass") {
		t.Fatal("plain list mentions bypass")
	}
}

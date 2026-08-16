// Package tui renders aiacc's interactive profile launcher: a framed, poka-yoke
// terminal UI shown by a bare `aiacc`. Each row is a profile (an isolated config
// directory); Enter launches the provider's CLI with that profile's directory,
// scoped to that run. There is no persistent "active" account and no shell env to
// mutate, so there is no shell hook — the confusing indirection is gone.
//
// Poka-yoke shapes the flow:
//   - Navigation is a bounded index. No text field on the list, so no parse step
//     and no invalid input.
//   - A profile you cannot launch (its dir is gone, or its provider has no
//     launch command) is shown but Enter is inert on it — the launch that can't
//     work is refused, not attempted. It can still be removed.
//   - Removing asks for an explicit `y`; Enter is inert there, so a stray key
//     can't drop a profile.
//
// Raw mode is toggled with stty (POSIX; the Linux/macOS release targets), so the
// package needs no non-stdlib dependency.
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"
)

// Row is one profile in the launcher. The caller fills it from config.
type Row struct {
	Provider  string
	Account   string
	Email     string // logged-in identity for the dir, "" when not logged in
	Dir       string // expanded config dir, passed to the launched CLI
	DirExists bool   // the config dir is present on disk
	EnvVar    string // env var the CLI reads to select its config ("" = unknown)
	Command   string // CLI to launch (e.g. "claude"); "" = no launcher for this
}

// launchable reports whether Enter can launch this profile: the dir exists, and
// we know both which env var to set and which command to run.
func (r Row) launchable() bool {
	return r.DirExists && r.EnvVar != "" && r.Command != ""
}

// blockedReason is the short label shown when a row cannot be launched.
func (r Row) blockedReason() string {
	switch {
	case r.EnvVar == "":
		return "no env var"
	case r.Command == "":
		return "no launcher"
	case !r.DirExists:
		return "dir missing"
	default:
		return ""
	}
}

// ResultKind is what the user asked the launcher to do.
type ResultKind int

const (
	Cancelled ResultKind = iota // backed out (q / Esc / Ctrl-C / EOF)
	Launch                      // launch Index's profile
	Add                         // pressed `a`; the caller runs the add screen
	Rename                      // pressed `r`; the caller runs the rename screen on Index
	Handoff                     // pressed `h`; the caller runs the handoff flow from Index
	Remove                      // confirmed removing Index
	Setup                       // pressed `s`; the caller runs shell setup
)

// Result is what Run/drive returns. Index is meaningful for Launch and Remove.
type Result struct {
	Kind  ResultKind
	Index int
}

const (
	clearHome  = "\x1b[2J\x1b[H"
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

// color is off when NO_COLOR is set (https://no-color.org).
var color = os.Getenv("NO_COLOR") == ""

// Cyberpunk / Akira palette: neon on black, with the iconic blood-red accent.
const (
	inkDim    = "38;5;240" // dim grey-blue
	inkGreen  = "38;5;48"  // neon green — active / ready
	inkYellow = "38;5;220" // neon amber — warnings, "not logged in"
	inkRed    = "38;5;196" // Akira red — the frame, blocked, danger
	inkBlue   = "38;5;51"  // neon cyan — commands, paths
	inkGrey   = "38;5;245" // muted label
	inkWhite  = "38;5;231" // bright white
	inkPink   = "38;5;201" // neon magenta — cursor, caret
)

// inkBorder is the frame colour — Akira red.
const inkBorder = inkRed

// seg is a run of text with an optional colour. Keeping colour separate from
// text lets width calculations run on visible runes, so ANSI escapes never leak
// into the maths that keeps the frame square.
type seg struct {
	text string
	ink  string
}

func pad(n int) seg { return seg{strings.Repeat(" ", max(0, n)), ""} }

func paint(s, ink string) string {
	if !color || ink == "" {
		return s
	}
	return "\x1b[" + ink + "m" + s + "\x1b[0m"
}

// render joins segs to exactly width visible runes — padding when short,
// truncating when long. This clamp is poka-yoke on the layout itself: no row,
// however long its content grows, can push the frame's border out.
func render(segs []seg, width int) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		if used >= width {
			break
		}
		t := s.text
		if utf8.RuneCountInString(t) > width-used {
			t = string([]rune(t)[:width-used])
		}
		used += utf8.RuneCountInString(t)
		b.WriteString(paint(t, s.ink))
	}
	if used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return b.String()
}

// Heavy box-drawing for a hard, cyber-terminal frame.
const (
	glTL, glTR, glBL, glBR = "┏", "┓", "┗", "┛"
	glH, glV               = "━", "┃"
)

// boxTop draws the top border: a red heavy rule with the title inset in white.
func boxTop(title string, w int) string {
	t := " " + strings.ToUpper(title) + " "
	dashes := strings.Repeat(glH, max(0, w-utf8.RuneCountInString(t)-1))
	return paint(glTL+glH, inkBorder) + paint(t, inkWhite) + paint(dashes+glTR, inkBorder)
}
func boxBottom(w int) string {
	return paint(glBL+strings.Repeat(glH, w)+glBR, inkBorder)
}
func boxRow(segs []seg, w int) string {
	return paint(glV, inkBorder) + render(segs, w) + paint(glV, inkBorder)
}
func boxBlank(w int) string { return boxRow(nil, w) }

func innerWidth(cols int) int {
	const base, floor = 46, 30
	w := base
	if cols-4 < w {
		w = cols - 4
	}
	return max(floor, w)
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func trunc(s string, n int) string {
	if runeLen(s) <= n {
		return s
	}
	return string([]rune(s)[:max(0, n-1)]) + "…"
}

// frame centres the built lines in the terminal, adds a clear+home, and appends
// a key legend below the box. Shared by the launcher, add, and remove screens.
func frame(lines []string, legend string, cols, innerW int) string {
	lines = append(lines, "", legend)
	left := max(0, (cols-(innerW+2))/2)
	pref := strings.Repeat(" ", left)
	var b strings.Builder
	b.WriteString(clearHome)
	b.WriteString("\r\n")
	for _, ln := range lines {
		b.WriteString(pref)
		b.WriteString(ln)
		b.WriteString("\r\n")
	}
	return b.String()
}

func legendLine(pairs [][2]string) string {
	var b strings.Builder
	b.WriteString("  ")
	for _, p := range pairs {
		b.WriteString(paint(p[0], inkBlue)) // neon-cyan keys
		b.WriteString(paint(" "+p[1]+"   ", inkDim))
	}
	return b.String()
}

// Render returns the launcher frame: one line per profile — a cursor mark, the
// provider·account, and a quiet login/status on the right. When setupNeeded is
// true (the shortcut commands aren't installed) it shows a one-line nudge toward
// `s`. Pure, for tests.
func Render(rows []Row, cursor int, setupNeeded bool, cols int) string {
	w := innerWidth(cols)
	const nameCol = 22

	lines := []string{boxTop("aiacc — launch a profile", w), boxBlank(w)}

	if setupNeeded {
		lines = append(lines,
			boxRow([]seg{pad(2), {"press ", inkDim}, {"s", inkYellow}, {" to install the shortcut commands", inkDim}}, w),
			boxBlank(w),
		)
	}

	if len(rows) == 0 {
		lines = append(lines,
			boxRow([]seg{pad(2), {"No profiles yet.", inkWhite}}, w),
			boxRow([]seg{pad(2), {"Press ", inkGrey}, {"a", inkWhite}, {" to add one.", inkGrey}}, w),
			boxBlank(w),
		)
	}

	for i, r := range rows {
		focused := i == cursor
		blocked := !r.launchable()

		cur := seg{"  ", ""}
		if focused {
			cur = seg{"▸ ", inkPink}
		}
		warn := ""
		nameInk := inkGrey
		switch {
		case blocked:
			warn, nameInk = "⚠ ", inkRed
		case focused:
			nameInk = inkWhite
		}
		// The account name is the launcher command, and the identity, so it is
		// the row's label. The provider is an implementation detail (which env
		// var / CLI) — shown as a dim tag only when it isn't the default claude.
		name := r.Account
		if r.Provider != "claude" {
			name += " (" + r.Provider + ")"
		}

		var info seg
		switch {
		case blocked:
			info = seg{r.blockedReason(), inkRed}
		case r.Email == "":
			info = seg{"not logged in", inkYellow}
		default:
			info = seg{trunc(r.Email, w-nameCol-4), inkGrey}
		}

		lines = append(lines, boxRow([]seg{
			cur,
			{warn, inkRed},
			{name, nameInk},
			pad(nameCol - runeLen(warn) - runeLen(name)),
			info,
		}, w))
	}

	lines = append(lines, boxBottom(w))
	pairs := [][2]string{{"↑↓", "move"}, {"⏎", "launch"}, {"a", "add"}, {"r", "rename"}, {"h", "hand off"}, {"d", "remove"}}
	if setupNeeded {
		pairs = append(pairs, [2]string{"s", "setup"})
	}
	pairs = append(pairs, [2]string{"q", "quit"})
	return frame(lines, legendLine(pairs), cols, w)
}

// RenderRemove is the confirm screen for removing a profile. Removing only
// unregisters it from aiacc; the config dir is left on disk, so it is reversible
// by re-adding. It asks first — an explicit `y`, never Enter.
func RenderRemove(r Row, cols int) string {
	w := innerWidth(cols)
	lines := []string{
		boxTop("aiacc — remove profile", w), boxBlank(w),
		boxRow([]seg{pad(2), {"Remove ", inkWhite}, {r.Account, inkYellow}, {" from aiacc?", inkWhite}}, w),
		boxBlank(w),
		boxRow([]seg{pad(2), {"The config dir is left on disk — only the", inkGrey}}, w),
		boxRow([]seg{pad(2), {"aiacc registration is removed.", inkGrey}}, w),
		boxBottom(w),
	}
	legend := legendLine([][2]string{{"y", "remove"}, {"n", "cancel"}})
	return frame(lines, legend, cols, w)
}

// --- Shell setup --------------------------------------------------------------

// SetupResult is what a one-step `aiacc setup` installed, for the result screen.
type SetupResult struct {
	BinDir   string   // display path the launchers were written to
	Names    []string // installed command names
	Example  string   // an example command to try
	WorksNow bool     // BinDir is on PATH → usable immediately, no reload
}

// RenderSetupResult returns the framed outcome of a one-step setup: what was
// installed and whether it works now. Pure, for tests.
func RenderSetupResult(r SetupResult, cols int) string {
	w := innerWidth(cols)
	lines := []string{
		boxTop("aiacc — setup", w), boxBlank(w),
		boxRow([]seg{pad(2), {"✓ ", inkGreen}, {strconv.Itoa(len(r.Names)) + " command(s) installed in", inkWhite}}, w),
		boxRow([]seg{pad(4), {r.BinDir, inkGrey}}, w),
		boxBlank(w),
	}
	for i, n := range r.Names {
		if i >= 8 { // don't let an absurd count tear the frame
			lines = append(lines, boxRow([]seg{pad(4), {"…and " + strconv.Itoa(len(r.Names)-8) + " more", inkDim}}, w))
			break
		}
		lines = append(lines, boxRow([]seg{pad(4), {n, inkBlue}}, w))
	}
	lines = append(lines, boxBlank(w))
	if r.WorksNow {
		lines = append(lines, boxRow([]seg{pad(2), {"They work now — try: ", inkWhite}, {r.Example, inkBlue}}, w))
	} else {
		lines = append(lines, boxRow([]seg{pad(2), {"Open a new terminal, then: ", inkWhite}, {r.Example, inkBlue}}, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"q", "done"}}), cols, w)
}

// driveSetupResult shows the result and waits for an exit key.
func driveSetupResult(r SetupResult, cols int, in io.Reader, out io.Writer) error {
	rd := bufio.NewReader(in)
	for {
		fmt.Fprint(out, RenderSetupResult(r, cols))
		k, err := readKey(rd)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if k == keyQuit || k == keyEnter {
			return nil
		}
	}
}

// RunSetupResult shows the setup result screen on /dev/tty.
func RunSetupResult(r SetupResult) error {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return err
	}
	defer restore()
	return driveSetupResult(r, cols, tty, tty)
}

// --- Add profile --------------------------------------------------------------

// AddResult is the outcome of the framed add screen. OK is false when cancelled.
type AddResult struct {
	Name string
	Dir  string
	OK   bool
}

// validName reports whether s is a legal profile name: a letter/underscore, then
// letters, digits, hyphens, or underscores. The name is also the launcher
// command, so it must be a legal shell function name — which is why a name like
// "??????????" cannot be entered.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case (r >= '0' && r <= '9' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

func nameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}
func printableByte(b byte) bool { return b >= 0x20 && b < 0x7f }

func defaultDir(name string) string {
	if name == "" {
		return "~/.claude-<name>"
	}
	return "~/.claude-" + name
}

// RenderAdd returns the add-profile frame: a live name field, a dir field that
// defaults to ~/.claude-<name> until edited, the current login for context, and
// an optional hint. field is 0 for name, 1 for dir. Pure, for tests.
func RenderAdd(currentLogin, name, dir string, field int, hint string, cols int) string {
	w := innerWidth(cols)
	lines := []string{boxTop("aiacc — add profile", w), boxBlank(w)}

	if currentLogin != "" {
		lines = append(lines,
			boxRow([]seg{pad(2), {"current  ", inkGrey}, {trunc(currentLogin, w-11), inkDim}}, w),
			boxBlank(w),
		)
	}

	nameCaret, dirCaret := "", ""
	if field == 0 {
		nameCaret = "▏"
	} else {
		dirCaret = "▏"
	}
	nameLabelInk, dirLabelInk := inkGrey, inkGrey
	if field == 0 {
		nameLabelInk = inkWhite
	} else {
		dirLabelInk = inkWhite
	}

	lines = append(lines, boxRow([]seg{
		pad(2), {"name  ", nameLabelInk}, {name, inkWhite}, {nameCaret, inkPink},
	}, w))

	dirSeg := seg{dir, inkWhite}
	if dir == "" {
		dirSeg = seg{defaultDir(name), inkDim}
	}
	lines = append(lines, boxRow([]seg{
		pad(2), {"dir   ", dirLabelInk}, dirSeg, {dirCaret, inkPink},
	}, w))

	lines = append(lines, boxBlank(w))
	if hint != "" {
		lines = append(lines, boxRow([]seg{pad(2), {"⚠ " + trunc(hint, w-4), inkRed}}, w))
	} else {
		lines = append(lines, boxBlank(w))
	}
	lines = append(lines, boxBottom(w))

	// The name becomes the launcher command, so spell that out.
	tip := "type name"
	if validName(name) {
		tip = "launches as: " + name
	}
	legend := legendLine([][2]string{{"", tip}, {"⇥", "field"}, {"⏎", "create"}, {"esc", "cancel"}})
	return frame(lines, legend, cols, w)
}

// driveAdd is the add-screen input loop, decoupled from /dev/tty for tests. It
// filters keystrokes so the name field can only ever hold command-safe chars.
func driveAdd(currentLogin string, cols int, in io.Reader, out io.Writer) (AddResult, error) {
	name, dir, field, hint := "", "", 0, ""
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, RenderAdd(currentLogin, name, dir, field, hint, cols))
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				return AddResult{}, nil
			}
			return AddResult{}, err
		}
		switch b {
		case 0x1b, 3: // Esc / Ctrl-C
			return AddResult{}, nil
		case '\t':
			field, hint = 1-field, ""
		case '\r', '\n':
			if !validName(name) {
				field, hint = 0, "name: a letter first, then letters, digits, - or _"
				continue
			}
			d := dir
			if d == "" {
				d = "~/.claude-" + name
			}
			return AddResult{Name: name, Dir: d, OK: true}, nil
		case 0x7f, 0x08: // Backspace / Delete
			if field == 0 {
				name = trimLastByte(name)
			} else {
				dir = trimLastByte(dir)
			}
			hint = ""
		default:
			if field == 0 && nameByte(b) {
				name += string(b)
			} else if field == 1 && printableByte(b) {
				dir += string(b)
			}
			hint = ""
		}
	}
}

func trimLastByte(s string) string {
	if s == "" {
		return s
	}
	return s[:len(s)-1]
}

// --- Rename profile -----------------------------------------------------------

// RenderRename returns the rename screen: a single name field prefilled with the
// current name, plus a hint. Pure, for tests.
func RenderRename(oldName, buf, hint string, cols int) string {
	w := innerWidth(cols)
	lines := []string{
		boxTop("aiacc — rename profile", w), boxBlank(w),
		boxRow([]seg{pad(2), {"from  ", inkGrey}, {oldName, inkDim}}, w),
		boxRow([]seg{pad(2), {"to    ", inkWhite}, {buf, inkWhite}, {"▏", inkPink}}, w),
		boxBlank(w),
	}
	if hint != "" {
		lines = append(lines, boxRow([]seg{pad(2), {"⚠ " + trunc(hint, w-4), inkRed}}, w))
	} else {
		lines = append(lines, boxRow([]seg{pad(2), {"the command becomes: " + launchName(buf), inkBlue}}, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"", "edit name"}, {"⏎", "rename"}, {"esc", "cancel"}}), cols, w)
}

func launchName(buf string) string {
	if buf == "" {
		return "<name>"
	}
	return buf
}

// driveRename edits the name of a profile. taken is the set of other account
// names; the new name must be valid and not already in use. Returns the new name,
// or "" when cancelled (including an unchanged name — the caller no-ops either).
func driveRename(oldName string, taken map[string]bool, cols int, in io.Reader, out io.Writer) (string, error) {
	buf, hint := oldName, ""
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, RenderRename(oldName, buf, hint, cols))
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				return "", nil
			}
			return "", err
		}
		switch b {
		case 0x1b, 3: // Esc / Ctrl-C
			return "", nil
		case '\r', '\n':
			switch {
			case buf == oldName:
				return "", nil // unchanged — nothing to do
			case !validName(buf):
				hint = "name: a letter first, then letters, digits, - or _"
			case taken[buf]:
				hint = "a profile named " + buf + " already exists"
			default:
				return buf, nil
			}
		case 0x7f, 0x08:
			buf, hint = trimLastByte(buf), ""
		default:
			if nameByte(b) {
				buf += string(b)
			}
			hint = ""
		}
	}
}

// --- Generic list + message screens ------------------------------------------

// ListItem is one row of a generic selection list.
type ListItem struct {
	Primary   string // the main label
	Secondary string // a dim detail on the right (optional)
}

// RenderList returns a framed single-column selection list. Pure, for tests.
func RenderList(title string, items []ListItem, cursor, cols int) string {
	w := innerWidth(cols)
	const primCol = 20
	lines := []string{boxTop(title, w), boxBlank(w)}
	if len(items) == 0 {
		lines = append(lines, boxRow([]seg{pad(2), {"nothing to choose", inkDim}}, w), boxBlank(w))
	}
	for i, it := range items {
		focused := i == cursor
		cur := seg{"  ", ""}
		if focused {
			cur = seg{"▸ ", inkPink}
		}
		ink := inkGrey
		if focused {
			ink = inkWhite
		}
		row := []seg{cur, {trunc(it.Primary, primCol), ink}}
		if it.Secondary != "" {
			row = append(row, pad(primCol-runeLen(trunc(it.Primary, primCol))+1), seg{it.Secondary, inkDim})
		}
		lines = append(lines, boxRow(row, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"↑↓", "move"}, {"⏎", "select"}, {"q", "cancel"}}), cols, w)
}

// driveList is the selection loop, decoupled from /dev/tty for tests. Returns the
// chosen index, or -1 if cancelled.
func driveList(title string, items []ListItem, cols int, in io.Reader, out io.Writer) (int, error) {
	cursor := 0
	if len(items) == 0 {
		cursor = -1
	}
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, RenderList(title, items, cursor, cols))
		k, err := readKey(r)
		if err != nil {
			if err == io.EOF {
				return -1, nil
			}
			return -1, err
		}
		switch k {
		case keyUp:
			if cursor > 0 {
				cursor--
			}
		case keyDown:
			if cursor >= 0 && cursor < len(items)-1 {
				cursor++
			}
		case keyEnter:
			if cursor >= 0 {
				return cursor, nil
			}
		case keyQuit:
			return -1, nil
		}
	}
}

// RunList shows a selection list on /dev/tty and returns the chosen index or -1.
func RunList(title string, items []ListItem) (int, error) {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return -1, err
	}
	defer restore()
	return driveList(title, items, cols, tty, tty)
}

// Line is one body line of a framed message. Color is an exported palette name
// (White, Grey, …) or "" for the default.
type Line struct {
	Text  string
	Color string
}

// Exported palette for callers building message bodies.
const (
	White = inkWhite
	Grey  = inkGrey
	Green = inkGreen
	Blue  = inkBlue
	Red   = inkRed
	Dim   = inkDim
)

// RenderMessage returns a framed message with a title and body lines. Pure.
func RenderMessage(title string, body []Line, cols int) string {
	w := innerWidth(cols)
	lines := []string{boxTop(title, w), boxBlank(w)}
	for _, l := range body {
		lines = append(lines, boxRow([]seg{pad(2), {l.Text, l.Color}}, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"q", "close"}}), cols, w)
}

// RunMessage shows a framed message on /dev/tty until an exit key.
func RunMessage(title string, body []Line) error {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return err
	}
	defer restore()
	r := bufio.NewReader(tty)
	for {
		fmt.Fprint(tty, RenderMessage(title, body, cols))
		k, err := readKey(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if k == keyQuit || k == keyEnter {
			return nil
		}
	}
}

// --- Terminal driving ---------------------------------------------------------

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyEnter
	keyAdd
	keyRename
	keyHandoff
	keyRemove
	keySetup
	keyYes
	keyNo
	keyQuit
)

// openRawTTY puts /dev/tty in raw mode and returns it, the terminal width, and a
// restore func. A signal handler restores the tty and shows the cursor before
// exiting on SIGTERM/HUP (or a SIGINT that slips past -isig), so no exit path
// leaves the user in a hidden-cursor raw terminal.
func openRawTTY() (*os.File, int, func(), error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	saved, err := sttyState(tty)
	if err != nil {
		tty.Close()
		return nil, 0, nil, err
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			stty(tty, saved)
			fmt.Fprint(tty, showCursor+clearHome)
			tty.Close()
		})
	}
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	// ponytail: this goroutine parks until a signal or process exit; a CLI runs a
	// couple of these at most, so the parked goroutine is not worth a teardown.
	go func() {
		<-sigc
		restore()
		os.Exit(130)
	}()
	if err := stty(tty, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		restore()
		return nil, 0, nil, err
	}
	fmt.Fprint(tty, hideCursor)
	return tty, ttyCols(tty), restore, nil
}

// Run shows the profile launcher on /dev/tty and returns the user's Result.
// setupNeeded surfaces the "install the shortcut commands" nudge.
func Run(rows []Row, setupNeeded bool) (Result, error) {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return Result{Kind: Cancelled}, err
	}
	defer restore()
	return drive(rows, setupNeeded, cols, tty, tty)
}

// RunAdd shows the framed add screen on /dev/tty. currentLogin (may be "") is
// shown for context.
func RunAdd(currentLogin string) (AddResult, error) {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return AddResult{}, err
	}
	defer restore()
	return driveAdd(currentLogin, cols, tty, tty)
}

// RunRename shows the rename screen on /dev/tty. taken is the set of other
// account names the new one may not collide with. Returns the new name, or "" if
// cancelled/unchanged.
func RunRename(oldName string, taken []string) (string, error) {
	set := make(map[string]bool, len(taken))
	for _, n := range taken {
		set[n] = true
	}
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return "", err
	}
	defer restore()
	return driveRename(oldName, set, cols, tty, tty)
}

// drive is the launcher render/key loop, decoupled from /dev/tty for tests. The
// cursor visits every row so any profile can be removed, but Enter only launches
// a launchable one — the guard is on the action, not the cursor.
func drive(rows []Row, setupNeeded bool, cols int, in io.Reader, out io.Writer) (Result, error) {
	cursor := initialCursor(rows)
	confirm := -1 // >=0 while confirming removal of that row
	r := bufio.NewReader(in)
	for {
		if confirm >= 0 {
			fmt.Fprint(out, RenderRemove(rows[confirm], cols))
		} else {
			fmt.Fprint(out, Render(rows, cursor, setupNeeded, cols))
		}
		k, err := readKey(r)
		if err != nil {
			if err == io.EOF {
				return Result{Kind: Cancelled}, nil
			}
			return Result{Kind: Cancelled}, err
		}
		if confirm >= 0 {
			switch k {
			case keyYes:
				return Result{Kind: Remove, Index: confirm}, nil
			case keyNo, keyQuit: // n / Esc / q / Ctrl-C back out — Enter is inert
				confirm = -1
			}
			continue
		}
		switch k {
		case keyUp:
			cursor = step(rows, cursor, -1)
		case keyDown:
			cursor = step(rows, cursor, +1)
		case keyEnter:
			if cursor >= 0 && rows[cursor].launchable() {
				return Result{Kind: Launch, Index: cursor}, nil
			}
		case keyRemove:
			if cursor >= 0 {
				confirm = cursor
			}
		case keyAdd:
			return Result{Kind: Add}, nil
		case keyRename:
			if cursor >= 0 {
				return Result{Kind: Rename, Index: cursor}, nil
			}
		case keyHandoff:
			if cursor >= 0 {
				return Result{Kind: Handoff, Index: cursor}, nil
			}
		case keySetup:
			return Result{Kind: Setup}, nil
		case keyQuit:
			return Result{Kind: Cancelled}, nil
		}
	}
}

// initialCursor lands on the first launchable profile, else the first row (so an
// all-blocked list is still navigable for removal), else -1 when empty.
func initialCursor(rows []Row) int {
	if len(rows) == 0 {
		return -1
	}
	for i, r := range rows {
		if r.launchable() {
			return i
		}
	}
	return 0
}

// step moves the cursor one row in direction dir, clamped to the list. Every row
// is reachable; launching is guarded at Enter, not here.
func step(rows []Row, cursor, dir int) int {
	if cursor < 0 {
		return cursor
	}
	next := cursor + dir
	if next < 0 || next >= len(rows) {
		return cursor
	}
	return next
}

// readKey decodes one logical keypress: arrows (or vim j/k), enter, add (a),
// remove (d), yes/no (y/n), and quit (q / Esc / Ctrl-C).
func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyNone, err
	}
	switch b {
	case '\r', '\n':
		return keyEnter, nil
	case 'q', 'Q', 3: // 3 = Ctrl-C (a byte under -isig)
		return keyQuit, nil
	case 'a', 'A':
		return keyAdd, nil
	case 'r', 'R':
		return keyRename, nil
	case 'h', 'H':
		return keyHandoff, nil
	case 'd', 'D':
		return keyRemove, nil
	case 's', 'S':
		return keySetup, nil
	case 'y', 'Y':
		return keyYes, nil
	case 'n', 'N':
		return keyNo, nil
	case 'j':
		return keyDown, nil
	case 'k':
		return keyUp, nil
	case 0x1b: // Esc: an arrow sequence, or a lone Esc that cancels
		b2, err := r.ReadByte()
		if err != nil {
			return keyQuit, nil
		}
		if b2 != '[' && b2 != 'O' {
			return keyNone, nil
		}
		b3, err := r.ReadByte()
		if err != nil {
			return keyNone, err
		}
		switch b3 {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		}
		return keyNone, nil
	}
	return keyNone, nil
}

func sttyState(tty *os.File) (string, error) {
	out, err := runStty(tty, "-g")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ttyCols reads the terminal width via `stty size`, falling back to 80.
func ttyCols(tty *os.File) int {
	out, err := runStty(tty, "size")
	if err != nil {
		return 80
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		if c, err := strconv.Atoi(f[1]); err == nil && c > 0 {
			return c
		}
	}
	return 80
}

func stty(tty *os.File, args ...string) error {
	_, err := runStty(tty, args...)
	return err
}

func runStty(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	out, err := cmd.Output()
	return string(out), err
}

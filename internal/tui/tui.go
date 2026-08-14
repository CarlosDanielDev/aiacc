// Package tui renders aiacc's interactive account UI: a framed, poka-yoke
// terminal front door shown by a bare `aiacc` and by `aiacc use` with no
// account. It draws on /dev/tty — stdout is reserved for the shell hook's
// command substitution — and returns the user's choice so the caller can emit
// the switch.
//
// Poka-yoke shapes the whole flow:
//   - Navigation is a bounded index. No text field, so no parse step, so no
//     invalid input to reject.
//   - A row you cannot safely switch into (its config dir is gone, or its
//     provider has no env var) is not selectable: the cursor skips it, so a
//     broken switch is unreachable rather than merely flagged.
//   - Switching only works through the shell hook. Rather than drop you into a
//     picker that can't apply anything and warn you after the fact, the caller
//     shows the setup gate (RunSetup) first when the hook is absent — so the
//     one confusing state, "I switched and nothing happened", cannot occur.
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

// Row is one account in the picker. The caller fills it from config + the live
// environment.
type Row struct {
	Provider  string
	Account   string
	Email     string // logged-in identity for the dir, "" when not logged in
	Dir       string // expanded, ready for the export line
	EnvVar    string // the provider's env var; "" means switching is undefined
	Active    bool   // this dir is the live one for its provider right now
	DirExists bool   // the config dir is present on disk
}

// ResultKind is what the user asked the picker to do.
type ResultKind int

const (
	Cancelled ResultKind = iota // backed out (q / Esc / Ctrl-C / EOF)
	Switch                      // chose Index; the caller emits its export line
	Add                         // pressed `a`; the caller runs the add wizard
	Remove                      // confirmed removing Index; the caller unregisters it
)

// Result is what Run/drive returns. Index is meaningful only for Switch.
type Result struct {
	Kind  ResultKind
	Index int
}

// rowState collapses a Row's flags into the single fact the UI cares about:
// whether it is switchable, and what to say when it is not.
type rowState int

const (
	stOK      rowState = iota // switchable, logged in
	stNoLogin                 // switchable, but no identity recorded yet
	stNoDir                   // blocked: the config dir is missing
	stNoEnv                   // blocked: the provider has no env var
)

func (r Row) state() rowState {
	switch {
	case r.EnvVar == "":
		return stNoEnv
	case !r.DirExists:
		return stNoDir
	case r.Email == "":
		return stNoLogin
	default:
		return stOK
	}
}

// selectable reports whether switching into this row is safe. Blocked rows are
// unreachable by the cursor — the mistake cannot be made, not merely flagged.
func (r Row) selectable() bool {
	s := r.state()
	return s != stNoDir && s != stNoEnv
}

const (
	clearHome  = "\x1b[2J\x1b[H"
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

// color is off when NO_COLOR is set (https://no-color.org).
var color = os.Getenv("NO_COLOR") == ""

const (
	inkDim    = "2"
	inkGreen  = "38;5;78"
	inkYellow = "38;5;221"
	inkRed    = "38;5;203"
	inkBlue   = "38;5;111"
	inkGrey   = "38;5;244"
	inkWhite  = "38;5;255"
	inkPink   = "38;5;218"
)

// seg is a run of text with an optional colour. Keeping colour separate from
// text lets every width calculation run on visible runes, so ANSI escapes never
// leak into the maths that keeps the frame square.
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
// however long its content grows, can push the frame's border out and tear the
// box. The guarantee lives here, not in every call site.
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

func boxTop(title string, w int) string {
	t := " " + title + " "
	return "╭─" + t + strings.Repeat("─", max(0, w-utf8.RuneCountInString(t)-1)) + "╮"
}
func boxBottom(w int) string { return "╰" + strings.Repeat("─", w) + "╯" }
func boxRow(segs []seg, w int) string {
	return "│" + render(segs, w) + "│"
}
func boxBlank(w int) string { return boxRow(nil, w) }

// innerWidth clamps the frame to the terminal but never collapses below a
// legible floor.
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
// a key legend below the box. Shared by the picker and the setup gate.
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

// Render returns the whole picker frame. One line per account: a cursor mark, a
// status dot (● active / ○ idle / ⚠ blocked), the provider·account, and a quiet
// login/status on the right. Pure, so tests assert on exact output.
func Render(rows []Row, cursor, cols int) string {
	w := innerWidth(cols)
	const nameCol = 22

	lines := []string{boxTop("aiacc — switch account", w), boxBlank(w)}

	if len(rows) == 0 {
		lines = append(lines,
			boxRow([]seg{pad(2), {"No accounts yet.", inkWhite}}, w),
			boxRow([]seg{pad(2), {"Press ", inkGrey}, {"a", inkWhite}, {" to add one.", inkGrey}}, w),
			boxBlank(w),
		)
	}

	for i, r := range rows {
		focused := i == cursor
		st := r.state()

		cur := seg{"  ", ""}
		if focused {
			cur = seg{"▸ ", inkPink}
		}
		var dot seg
		switch {
		case st == stNoDir || st == stNoEnv:
			dot = seg{"⚠ ", inkRed}
		case r.Active:
			dot = seg{"● ", inkGreen}
		default:
			dot = seg{"○ ", inkDim}
		}
		nameInk := inkGrey
		switch {
		case st == stNoDir || st == stNoEnv:
			nameInk = inkRed
		case focused:
			nameInk = inkWhite
		}
		name := r.Provider + " · " + r.Account

		var info seg
		switch st {
		case stNoEnv:
			info = seg{"no env var", inkRed}
		case stNoDir:
			info = seg{"dir missing", inkRed}
		case stNoLogin:
			info = seg{"not logged in", inkYellow}
		default:
			info = seg{trunc(r.Email, w-nameCol-4), inkGrey}
		}

		lines = append(lines, boxRow([]seg{
			cur, dot,
			{name, nameInk},
			pad(nameCol - runeLen(name)),
			info,
		}, w))
	}

	lines = append(lines, boxBottom(w))
	legend := legendLine([][2]string{{"↑↓", "move"}, {"⏎", "switch"}, {"a", "add"}, {"d", "remove"}, {"q", "quit"}})
	return frame(lines, legend, cols, w)
}

// RenderRemove is the confirm screen for removing an account. Removing only
// unregisters it from aiacc; the config dir is left on disk, so the action is
// reversible by re-adding. It still asks first — an explicit `y`, never Enter,
// so a stray keystroke can't drop an account.
func RenderRemove(r Row, cols int) string {
	w := innerWidth(cols)
	lines := []string{
		boxTop("aiacc — remove account", w), boxBlank(w),
		boxRow([]seg{pad(2), {"Remove ", inkWhite}, {r.Provider + " · " + r.Account, inkYellow}, {" from aiacc?", inkWhite}}, w),
		boxBlank(w),
		boxRow([]seg{pad(2), {"The config dir is left on disk — only the", inkGrey}}, w),
		boxRow([]seg{pad(2), {"aiacc registration is removed.", inkGrey}}, w),
		boxBottom(w),
	}
	legend := legendLine([][2]string{{"y", "remove"}, {"n", "cancel"}})
	return frame(lines, legend, cols, w)
}

func legendLine(pairs [][2]string) string {
	var b strings.Builder
	b.WriteString("  ")
	for _, p := range pairs {
		b.WriteString(paint(p[0], inkWhite))
		b.WriteString(paint(" "+p[1]+"   ", inkDim))
	}
	return b.String()
}

// --- Setup gate ---------------------------------------------------------------

// SetupInfo is what the gate needs to explain and (optionally) perform the
// one-time hook install.
type SetupInfo struct {
	Shell          string // bash | zsh | fish
	Line           string // the line to add to the startup file
	Path           string // display path of that startup file (~ form)
	AlreadyPresent bool   // the line is already there; only a restart is needed
}

type setupPhase int

const (
	phaseAsk     setupPhase = iota // offer to install
	phasePresent                   // line already present, restart to finish
	phaseDone                      // just installed, restart to finish
	phaseErr                       // install failed
)

// RenderSetup returns the gate frame for a phase. doneErr is shown in phaseErr.
func RenderSetup(info SetupInfo, phase setupPhase, doneErr error, cols int) string {
	w := innerWidth(cols)
	var lines []string
	var legend string

	switch phase {
	case phaseAsk:
		lines = []string{
			boxTop("aiacc — one-time setup", w), boxBlank(w),
			boxRow([]seg{pad(2), {"Switching accounts sets an environment", inkWhite}}, w),
			boxRow([]seg{pad(2), {"variable in your shell — that needs a", inkWhite}}, w),
			boxRow([]seg{pad(2), {"one-line hook, added once.", inkWhite}}, w),
			boxBlank(w),
			boxRow([]seg{pad(2), {"shell   ", inkGrey}, {info.Shell, inkWhite}}, w),
			boxRow([]seg{pad(2), {"add to  ", inkGrey}, {info.Path, inkWhite}}, w),
			boxRow([]seg{pad(4), {info.Line, inkBlue}}, w),
			boxBottom(w),
		}
		legend = legendLine([][2]string{{"i", "install it for me"}, {"q", "not now"}})
	case phasePresent:
		lines = []string{
			boxTop("aiacc — one-time setup", w), boxBlank(w),
			boxRow([]seg{pad(2), {"The hook is already in your startup file:", inkWhite}}, w),
			boxRow([]seg{pad(4), {info.Path, inkGrey}}, w),
			boxBlank(w),
			boxRow([]seg{pad(2), {"Open a new terminal to load it, then run", inkWhite}}, w),
			boxRow([]seg{pad(2), {"aiacc again.", inkWhite}}, w),
			boxBottom(w),
		}
		legend = legendLine([][2]string{{"q", "ok"}})
	case phaseDone:
		lines = []string{
			boxTop("aiacc — setup", w), boxBlank(w),
			boxRow([]seg{pad(2), {"✓ Added the hook to", inkGreen}}, w),
			boxRow([]seg{pad(4), {info.Path, inkWhite}}, w),
			boxBlank(w),
			boxRow([]seg{pad(2), {"Open a new terminal (or run:", inkWhite}}, w),
			boxRow([]seg{pad(4), {sourceCmd(info), inkBlue}}, w),
			boxRow([]seg{pad(2), {"then run ", inkWhite}, {"aiacc", inkWhite}, {" again.", inkWhite}}, w),
			boxBottom(w),
		}
		legend = legendLine([][2]string{{"q", "done"}})
	case phaseErr:
		msg := "unknown error"
		if doneErr != nil {
			msg = doneErr.Error()
		}
		lines = []string{
			boxTop("aiacc — setup", w), boxBlank(w),
			boxRow([]seg{pad(2), {"⚠ Couldn't write the hook:", inkRed}}, w),
			boxRow([]seg{pad(4), {trunc(msg, w-6), inkGrey}}, w),
			boxBlank(w),
			boxRow([]seg{pad(2), {"Add this line by hand to " + info.Path + ":", inkWhite}}, w),
			boxRow([]seg{pad(4), {info.Line, inkBlue}}, w),
			boxBottom(w),
		}
		legend = legendLine([][2]string{{"q", "ok"}})
	}
	return frame(lines, legend, cols, w)
}

func sourceCmd(info SetupInfo) string {
	return "source " + info.Path
}

// --- Add account --------------------------------------------------------------

// AddResult is the outcome of the framed add screen. OK is false when cancelled.
type AddResult struct {
	Name string
	Dir  string
	OK   bool
}

// validName reports whether s is a legal account name: a letter/underscore, then
// letters, digits, hyphens, or underscores. This is exactly the alias-safe set,
// so every account the screen creates gets a working `<provider>-<name>` shortcut
// — and a name like "??????????" cannot be entered at all.
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

// RenderAdd returns the add-account frame: a live name field, a dir field that
// defaults to ~/.claude-<name> until edited, the current login for context, and
// an optional hint. field is 0 for name, 1 for dir. Pure, for tests.
func RenderAdd(currentLogin, name, dir string, field int, hint string, cols int) string {
	w := innerWidth(cols)
	lines := []string{boxTop("aiacc — add account", w), boxBlank(w)}

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

	// The dir field shows the derived default (dim) until the user types one.
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

	legend := legendLine([][2]string{{"type", "name"}, {"⇥", "field"}, {"⏎", "create"}, {"esc", "cancel"}})
	return frame(lines, legend, cols, w)
}

// driveAdd is the add-screen input loop, decoupled from /dev/tty for tests. It
// filters keystrokes so the name field can only ever hold alias-safe characters.
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

// --- Terminal driving ---------------------------------------------------------

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyEnter
	keyAdd
	keyInstall
	keyRemove
	keyYes
	keyNo
	keyQuit
)

// openRawTTY puts /dev/tty in raw mode and returns it, the terminal width, and a
// restore func. A signal handler restores the tty and shows the cursor before
// exiting on SIGTERM/HUP (or a SIGINT that slips past -isig), so no exit path
// leaves the user in a hidden-cursor raw terminal. Cleanup is not a step you can
// forget, because it is not a step you perform.
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
	// -isig makes Ctrl-C arrive as a byte we handle as a clean cancel in-loop.
	if err := stty(tty, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		restore()
		return nil, 0, nil, err
	}
	fmt.Fprint(tty, hideCursor)
	return tty, ttyCols(tty), restore, nil
}

// Run shows the account picker on /dev/tty and returns the user's Result. Only
// reached when the shell hook is active, so a chosen switch always applies.
func Run(rows []Row) (Result, error) {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return Result{Kind: Cancelled}, err
	}
	defer restore()
	return drive(rows, cols, tty, tty)
}

// RunSetup shows the one-time setup gate. install performs the hook write and
// returns nil on success; the gate handles rendering the outcome.
func RunSetup(info SetupInfo, install func() error) error {
	tty, cols, restore, err := openRawTTY()
	if err != nil {
		return err
	}
	defer restore()
	return driveSetup(info, install, cols, tty, tty)
}

// drive is the picker render/key loop, decoupled from /dev/tty for tests. The
// cursor visits every row so any account can be removed, but Enter only switches
// into a selectable one — you still cannot switch into a broken account, the
// guard just moved from the cursor to the action.
func drive(rows []Row, cols int, in io.Reader, out io.Writer) (Result, error) {
	cursor := initialCursor(rows)
	confirm := -1 // >=0 while confirming removal of that row
	r := bufio.NewReader(in)
	for {
		if confirm >= 0 {
			fmt.Fprint(out, RenderRemove(rows[confirm], cols))
		} else {
			fmt.Fprint(out, Render(rows, cursor, cols))
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
			if cursor >= 0 && rows[cursor].selectable() {
				return Result{Kind: Switch, Index: cursor}, nil
			}
		case keyRemove:
			if cursor >= 0 {
				confirm = cursor
			}
		case keyAdd:
			return Result{Kind: Add}, nil
		case keyQuit:
			return Result{Kind: Cancelled}, nil
		}
	}
}

// driveSetup is the gate render/key loop, decoupled from /dev/tty for tests.
func driveSetup(info SetupInfo, install func() error, cols int, in io.Reader, out io.Writer) error {
	phase := phaseAsk
	if info.AlreadyPresent {
		phase = phasePresent
	}
	var doneErr error
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, RenderSetup(info, phase, doneErr, cols))
		k, err := readKey(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch phase {
		case phaseAsk:
			switch k {
			case keyInstall:
				if doneErr = install(); doneErr != nil {
					phase = phaseErr
				} else {
					phase = phaseDone
				}
			case keyQuit:
				return nil
			}
		default: // phasePresent, phaseDone, phaseErr — any exit key leaves
			if k == keyQuit || k == keyEnter {
				return nil
			}
		}
	}
}

// initialCursor lands on the active account, else the first selectable row, else
// the first row (so an all-blocked list is still navigable for removal), else -1
// when the list is empty.
func initialCursor(rows []Row) int {
	if len(rows) == 0 {
		return -1
	}
	first := -1
	for i, r := range rows {
		if r.Active {
			return i
		}
		if first < 0 && r.selectable() {
			first = i
		}
	}
	if first >= 0 {
		return first
	}
	return 0
}

// step moves the cursor one row in direction dir, clamped to the list. Every row
// is reachable; switching is guarded at Enter, not here.
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
// install (i), and quit (q / Esc / Ctrl-C).
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
	case 'i', 'I':
		return keyInstall, nil
	case 'd', 'D':
		return keyRemove, nil
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

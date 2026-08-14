// Package tui renders aiacc's interactive account picker: a framed, poka-yoke
// terminal UI shown by a bare `aiacc` (the front door) and by `aiacc use` with
// no account. It draws on /dev/tty — stdout is reserved for the shell hook's
// command substitution — and returns the chosen account so the caller can emit
// the switch.
//
// Poka-yoke is the whole point of this package:
//   - There is no text field, so there is no parse step, so there is no invalid
//     input to reject. Navigation is a bounded index.
//   - A row you cannot safely switch into (its config dir is gone, or its
//     provider has no env var) is *not selectable*: the cursor skips it, so the
//     broken switch is unreachable rather than merely warned about.
//   - When the shell hook is not active a switch would print an export line that
//     nothing evaluates — a silent no-op. The frame carries a persistent andon
//     banner in that state, so you learn the switch won't apply before you make
//     it, not after.
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
	"syscall"
	"unicode/utf8"
)

// Row is one account in the picker, with everything the poka-yoke rules and the
// display need. The caller fills it from config + live environment + logs.
type Row struct {
	Provider  string
	Account   string
	Email     string // logged-in identity for the dir, "" when not logged in
	Dir       string // expanded, ready for the export line
	EnvVar    string // the provider's env var; "" means switching is undefined
	Active    bool   // this dir is the live one for its provider right now
	DirExists bool   // the config dir is present on disk
	Tokens    int    // total tokens from the dir's session logs
	Quota     int    // manual plan size; 0 = unset
}

// ResultKind is what the user asked the picker to do.
type ResultKind int

const (
	// Cancelled: the user backed out (q / Esc / Ctrl-C / EOF). Emit nothing.
	Cancelled ResultKind = iota
	// Switch: the user chose Index; the caller emits its export line.
	Switch
	// Add: the user pressed `a`; the caller runs the add wizard and re-opens.
	Add
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

// 256-colour SGR codes, matching native-jiggler's palette.
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
// text lets every width calculation run on *visible* runes, so ANSI escapes
// never leak into the maths that keeps the frame square.
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
// truncating when long. This clamp is the poka-yoke turned on the layout code
// itself: no row, however long its content grows, can push the frame's border
// out and tear the box. The guarantee lives here, not in every call site.
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

// Box-drawing. Every glyph is one terminal column, so visible-rune padding keeps
// the frame square with no wcwidth guesswork.
func boxTop(title string, w int) string {
	t := " " + title + " "
	return "╭─" + t + strings.Repeat("─", max(0, w-utf8.RuneCountInString(t)-1)) + "╮"
}
func boxDivider(w int) string { return "├" + strings.Repeat("─", w) + "┤" }
func boxBottom(w int) string  { return "╰" + strings.Repeat("─", w) + "╯" }
func boxRow(segs []seg, w int) string {
	return "│" + render(segs, w) + "│"
}
func boxBlank(w int) string { return boxRow(nil, w) }

// miniBar renders tokens as a filled bar relative to the busiest account. The
// shape is honest: it is derived from real token totals, not a fabricated time
// series aiacc does not keep. Colour tracks the same green→yellow→red urgency
// as the quota cell.
func miniBar(tokens, maxTokens, cells int) []seg {
	if maxTokens <= 0 || tokens <= 0 {
		return []seg{{strings.Repeat("·", cells), inkDim}}
	}
	frac := float64(tokens) / float64(maxTokens)
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(cells) + 0.5)
	if filled > cells {
		filled = cells
	}
	ink := inkGreen
	switch {
	case frac > 0.85:
		ink = inkRed
	case frac > 0.6:
		ink = inkYellow
	}
	return []seg{
		{strings.Repeat("█", filled), ink},
		{strings.Repeat("░", cells-filled), inkDim},
	}
}

// quotaCell shows used/quota as a coloured percentage, or "-" when no quota is
// set. It never divides by zero and clamps a runaway percentage to a legible
// ">999%" rather than smearing the frame.
func quotaCell(tokens, quota int) seg {
	if quota <= 0 {
		return seg{"    -", inkDim}
	}
	pct := tokens * 100 / quota
	ink := inkGreen
	switch {
	case pct > 85:
		ink = inkRed
	case pct > 60:
		ink = inkYellow
	}
	s := strconv.Itoa(pct) + "%"
	if pct > 999 {
		s = ">999%"
	}
	return seg{fmt.Sprintf("%5s", s), ink}
}

// humanTokens renders a token count compactly (1_200_000 -> "1.2M").
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return strconv.Itoa(n)
	}
}

// innerWidth is the frame's inner column count, clamped so it fits the terminal
// but never collapses below a legible floor.
func innerWidth(cols int) int {
	const base, floor = 44, 30
	w := base
	if cols-4 < w {
		w = cols - 4
	}
	return max(floor, w)
}

// Render returns the whole terminal frame: a clear+home, the centred box, and a
// key legend below it. It is pure (no I/O) so tests can assert on the exact
// output for any width and any row state.
func Render(rows []Row, cursor int, hookActive bool, cols int) string {
	w := innerWidth(cols)

	maxTokens := 0
	for _, r := range rows {
		if r.Tokens > maxTokens {
			maxTokens = r.Tokens
		}
	}

	var lines []string
	lines = append(lines, boxTop("aiacc", w), boxBlank(w))

	if len(rows) == 0 {
		lines = append(lines,
			boxRow([]seg{pad(2), {"No accounts registered yet.", inkWhite}}, w),
			boxBlank(w),
			boxRow([]seg{pad(2), {"Press ", inkGrey}, {"a", inkWhite}, {" to add one, or run:", inkGrey}}, w),
			boxRow([]seg{pad(4), {"aiacc add <provider> <account> --dir <path>", inkBlue}}, w),
			boxBlank(w),
		)
	}

	for i, r := range rows {
		focused := i == cursor
		st := r.state()

		// Line 1: cursor + provider·account + status badge.
		marker := seg{"  ", ""}
		if focused {
			marker = seg{"▸ ", inkPink}
		}
		nameInk := inkGrey
		if focused {
			nameInk = inkWhite
		}
		warn := ""
		if st == stNoDir || st == stNoEnv {
			warn = "⚠ "
			nameInk = inkRed
		}
		line1 := []seg{
			marker,
			{warn, inkRed},
			{r.Provider + " · " + r.Account, nameInk},
		}
		switch {
		case r.Active:
			line1 = append(line1, pad(2), seg{"● ACTIVE", inkGreen})
		case st == stNoDir:
			line1 = append(line1, pad(2), seg{"dir missing", inkRed})
		case st == stNoEnv:
			line1 = append(line1, pad(2), seg{"no env var", inkRed})
		}
		lines = append(lines, boxRow(line1, w))

		// Line 2: identity + tokens + relative bar + quota. Blocked rows skip it;
		// there is nothing safe to act on, so the detail would only invite a
		// switch the UI is refusing to allow.
		if st == stNoDir || st == stNoEnv {
			continue
		}
		id := r.Email
		idInk := inkGrey
		if id == "" {
			id, idInk = "not logged in", inkYellow
		}
		line2 := []seg{
			pad(4),
			{fmt.Sprintf("%-18s", trunc(id, 18)), idInk},
			{fmt.Sprintf("%6s ", humanTokens(r.Tokens)), inkWhite},
		}
		line2 = append(line2, miniBar(r.Tokens, maxTokens, 4)...)
		line2 = append(line2, seg{" ", ""}, quotaCell(r.Tokens, r.Quota))
		lines = append(lines, boxRow(line2, w))
	}

	// Andon: the shell hook is what actually applies a switch. Without it, the
	// export line goes nowhere and switching is a convincing no-op — so say so,
	// continuously, inside the frame.
	if !hookActive {
		lines = append(lines,
			boxBlank(w),
			boxDivider(w),
			boxRow([]seg{pad(2), {"⚠ hook not active — switch won't apply", inkYellow}}, w),
			boxRow([]seg{pad(4), {"fix: ", inkGrey}, {`eval "$(aiacc shell-init <shell>)"`, inkBlue}}, w),
		)
	}

	lines = append(lines, boxBottom(w))

	// Key legend, outside the frame like native-jiggler's dashboard.
	legend := "  " + paint("↑↓", inkWhite) + paint(" move   ", inkDim) +
		paint("⏎", inkWhite) + paint(" switch   ", inkDim) +
		paint("a", inkWhite) + paint(" add   ", inkDim) +
		paint("q", inkWhite) + paint(" quit", inkDim)
	lines = append(lines, "", legend)

	// Centre horizontally; a small top margin keeps it off the very top row.
	left := max(0, (cols-(w+2))/2)
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

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// key is one decoded logical keypress.
type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyEnter
	keyAdd
	keyQuit
)

// Run shows the picker on /dev/tty and returns the user's Result. hookActive
// tells it whether a switch will actually be applied (see the andon in Render).
//
// Poka-yoke on the terminal itself: entering raw mode installs its own undo via
// a signal handler in the same breath. A SIGTERM/SIGHUP — or a SIGINT that slips
// past the -isig flag — restores the tty and shows the cursor before exiting, so
// no code path leaves the user staring at a hidden cursor in a raw terminal.
// Cleanup is not a step you can forget, because it is not a step you perform.
func Run(rows []Row, hookActive bool) (Result, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return Result{Kind: Cancelled}, err
	}
	defer tty.Close()

	saved, err := sttyState(tty)
	if err != nil {
		return Result{Kind: Cancelled}, err
	}
	restore := func() {
		stty(tty, saved)
		fmt.Fprint(tty, showCursor+clearHome)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigc:
			restore()
			os.Exit(130)
		case <-done:
		}
	}()
	defer close(done)
	defer signal.Stop(sigc)
	defer restore()

	// -isig makes Ctrl-C arrive as a byte we handle as a clean cancel in-loop,
	// rather than a signal that would race the restore above.
	if err := stty(tty, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		return Result{Kind: Cancelled}, err
	}
	fmt.Fprint(tty, hideCursor)

	return drive(rows, hookActive, ttyCols(tty), tty, tty)
}

// drive is the render/key loop, decoupled from /dev/tty so tests can feed it
// scripted keystrokes and a fixed width.
func drive(rows []Row, hookActive bool, cols int, in io.Reader, out io.Writer) (Result, error) {
	cursor := initialCursor(rows)
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, Render(rows, cursor, hookActive, cols))
		k, err := readKey(r)
		if err != nil {
			if err == io.EOF {
				return Result{Kind: Cancelled}, nil
			}
			return Result{Kind: Cancelled}, err
		}
		switch k {
		case keyUp:
			cursor = step(rows, cursor, -1)
		case keyDown:
			cursor = step(rows, cursor, +1)
		case keyEnter:
			// Guaranteed selectable: the cursor only ever lands on selectable
			// rows, so there is no broken switch to guard against here.
			if cursor >= 0 {
				return Result{Kind: Switch, Index: cursor}, nil
			}
		case keyAdd:
			return Result{Kind: Add}, nil
		case keyQuit:
			return Result{Kind: Cancelled}, nil
		}
	}
}

// initialCursor lands on the active account if it is selectable, else the first
// selectable row, else -1 when nothing can be switched into.
func initialCursor(rows []Row) int {
	first := -1
	for i, r := range rows {
		if !r.selectable() {
			continue
		}
		if first < 0 {
			first = i
		}
		if r.Active {
			return i
		}
	}
	return first
}

// step moves the cursor to the nearest selectable row in direction dir, or stays
// put when none exists that way. The boundary is where movement stops — you
// cannot land on a blocked row.
func step(rows []Row, cursor, dir int) int {
	if cursor < 0 {
		return cursor
	}
	for i := cursor + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].selectable() {
			return i
		}
	}
	return cursor
}

// readKey decodes one logical keypress: arrows (or vim j/k), enter, add (a),
// and quit (q / Esc / Ctrl-C).
func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyNone, err
	}
	switch b {
	case '\r', '\n':
		return keyEnter, nil
	case 'q', 'Q', 3: // 3 = Ctrl-C (delivered as a byte under -isig)
		return keyQuit, nil
	case 'a', 'A':
		return keyAdd, nil
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

// sttyState captures the current terminal settings so they can be restored.
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

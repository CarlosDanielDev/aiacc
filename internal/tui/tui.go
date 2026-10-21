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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// Row is one profile in the launcher. The caller fills it from config.
type Row struct {
	Provider  string
	Account   string
	Email     string   // logged-in identity for the dir, "" when not logged in
	Dir       string   // expanded config dir, passed to the launched CLI
	DirExists bool     // the config dir is present on disk
	EnvVar    string   // env var the CLI reads to select its config ("" = unknown)
	Command   string   // CLI to launch (e.g. "claude"); "" = no launcher for this
	Args      []string // passed to Command ahead of anything else (e.g. --settings <file>)
	Settings  string   // extra settings file the launcher passes, shown as a tag; "" = none
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
	Edit                        // pressed `e`; the caller runs the edit screen on Index
	Resume                      // pressed `u`; the caller resumes a session of Index in place
	Duplicate                   // pressed `c`; the caller runs the duplicate screen on Index
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

// Hacker / matrix phosphor palette: green on black, cyan for interaction.
const (
	inkDim    = "38;5;238" // near-black green-grey
	inkGreen  = "38;5;46"  // bright phosphor green — active / ready / prompt
	inkYellow = "38;5;220" // amber — warnings, "not logged in"
	inkRed    = "38;5;196" // alert red — blocked, danger
	inkBlue   = "38;5;51"  // cyan — keys, commands, paths
	inkGrey   = "38;5;245" // muted label
	inkWhite  = "38;5;231" // bright white
	inkPink   = "38;5;48"  // spring green — cursor, caret
)

// inkBorder is the frame colour — a solid terminal green.
const inkBorder = "38;5;34"

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
	glML, glMR             = "┣", "┫"
	glH, glV               = "━", "┃"
)

// boxDivider is an inner section rule.
func boxDivider(w int) string {
	return paint(glML+strings.Repeat(glH, w)+glMR, inkBorder)
}

// boxRaw places already-coloured content (with known visible width) into a row,
// padding to the frame width. Used for the gradient logo, whose ANSI escapes
// render() cannot measure.
func boxRaw(content string, visibleW, w int) string {
	return paint(glV, inkBorder) + content + strings.Repeat(" ", max(0, w-visibleW)) + paint(glV, inkBorder)
}

// boxRowHi is a highlighted (reverse-video) row — the selection "glow".
func boxRowHi(text string, w int) string {
	body := render([]seg{{text, ""}}, w) // plain, padded/truncated to exactly w
	if color {
		body = "\x1b[7m" + body + "\x1b[27m"
	}
	return paint(glV, inkBorder) + body + paint(glV, inkBorder)
}

// neonRamp is the logo's phosphor sweep: dark green → bright green → cyan.
var neonRamp = []string{"38;5;22", "38;5;28", "38;5;34", "38;5;40", "38;5;46", "38;5;48", "38;5;51"}

// logoRows is a half-block "AIACC" wordmark.
var logoRows = []string{
	"▄▀█ █ ▄▀█ █▀▀ █▀▀",
	"█▀█ █ █▀█ █▄▄ █▄▄",
}

// gradient colours each non-space rune of s along ramp, offset by shift.
func gradient(s string, ramp []string, shift int) string {
	var b strings.Builder
	i := 0
	for _, r := range s {
		if r == ' ' {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(paint(string(r), ramp[(i+shift)%len(ramp)]))
		i++
	}
	return b.String()
}

var glitchChars = []rune("▓▒░█▚▞╱╲▘▝")

// glitchLogo is a corrupted render of one logo row: a deterministic subset of
// cells is swapped for glitch glyphs in clashing neon, for an Akira data-tear
// flicker. Deterministic in phase so the render stays testable, and every
// substitute is a single column so the frame never tears.
func glitchLogo(s string, phase int) string {
	var b strings.Builder
	i := 0
	for _, r := range s {
		if r == ' ' {
			b.WriteByte(' ')
			continue
		}
		h := (phase*131 + i*17) & 0xff
		switch {
		case h < 50: // ~20% of cells corrupt this frame
			b.WriteString(paint(string(glitchChars[h%len(glitchChars)]), inkBlue))
		case h < 80: // a channel-split flash
			b.WriteString(paint(string(r), inkWhite))
		default:
			b.WriteString(paint(string(r), neonRamp[(i+phase)%len(neonRamp)]))
		}
		i++
	}
	return b.String()
}

// logoArt returns the (possibly glitched) coloured logo row for a phase. It
// glitches in a short burst near the end of each ~14-frame cycle; phase 0 (the
// static render) is always clean.
func logoArt(row string, phase int) string {
	if phase%14 >= 12 {
		return glitchLogo(row, phase)
	}
	return gradient(row, neonRamp, phase)
}

// boxTop draws the top border: a heavy rule with the title inset in white. The
// title is clamped like any other row — a title longer than the frame is the one
// piece of content that could still push a border out, so it is cut, not kept.
func boxTop(title string, w int) string {
	t := " " + strings.ToUpper(title) + " "
	if runeLen(t) > w-1 {
		t = trunc(t, max(0, w-1))
	}
	dashes := strings.Repeat(glH, max(0, w-runeLen(t)-1))
	return paint(glTL+glH, inkBorder) + paint(t, inkWhite) + paint(dashes+glTR, inkBorder)
}
func boxBottom(w int) string {
	return paint(glBL+strings.Repeat(glH, w)+glBR, inkBorder)
}
func boxRow(segs []seg, w int) string {
	return paint(glV, inkBorder) + render(segs, w) + paint(glV, inkBorder)
}
func boxBlank(w int) string { return boxRow(nil, w) }

// hintRow is the refusal line a form shows under its fields: red, cut to the
// frame, or a blank row while there is no hint to show.
func hintRow(hint string, w int) string {
	if hint == "" {
		return boxBlank(w)
	}
	return boxRow([]seg{pad(2), {"⚠ " + trunc(hint, w-4), inkRed}}, w)
}

// Size is the terminal geometry a frame renders into. Zero values fall back to a
// conventional 80x24, so a pure render is always well-defined (tests, pipes, a
// terminal that won't report its size).
type Size struct{ Cols, Rows int }

func (s Size) cols() int {
	if s.Cols <= 0 {
		return 80
	}
	return s.Cols
}

func (s Size) rows() int {
	if s.Rows <= 0 {
		return 24
	}
	return s.Rows
}

// innerWidth is the frame's inner width in a terminal that wide. It grows with
// the terminal up to a comfortable maximum — and, the part that matters, never
// exceeds what the terminal can actually show. A box wider than the screen wraps,
// and a wrapped box tears: every following line lands one row low and the borders
// come apart. Clamping here is poka-yoke on that whole class of breakage.
func innerWidth(cols int) int {
	const narrowest, widest = 30, 60
	w := min(max(cols-4, narrowest), widest) // 1 margin + 1 border each side
	return max(8, min(w, cols-2))            // never wider than the terminal
}

// window returns the bounds of a scrolling viewport over n items, budget tall,
// that keeps cursor in view. It is the vertical twin of innerWidth's clamp: a
// list taller than the terminal scrolls the screen and tears the frame, so the
// list is windowed instead.
func window(n, cursor, budget int) (int, int) {
	if budget <= 0 || budget >= n {
		return 0, n
	}
	start := cursor - budget/2
	start = max(0, min(start, n-budget))
	return start, start + budget
}

// moreLine is the "there is more above/below" readout for a windowed list.
func moreLine(start, end, n int) []seg {
	out := []seg{pad(2)}
	if start > 0 {
		out = append(out, seg{"▲ " + strconv.Itoa(start) + " above", inkDim})
	}
	if start > 0 && end < n {
		out = append(out, seg{"  ·  ", inkDim})
	}
	if end < n {
		out = append(out, seg{"▼ " + strconv.Itoa(n-end) + " below", inkDim})
	}
	return out
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
// frame repaints in place: cursor home, each line cleared to its end, then
// everything below the frame cleared. No full-screen erase, so an animated
// redraw doesn't flicker and a shrinking frame leaves no residue.
func frame(lines []string, lg legend, sz Size, innerW int) string {
	left := max(0, (sz.cols()-(innerW+2))/2)
	pref := strings.Repeat(" ", left)
	// The last clamp, after every per-screen one: what is emitted always fits the
	// terminal. Overflowing the bottom scrolls the screen, which desynchronises
	// the cursor-home repaint and tears every frame after it.
	// The legend may wrap, but only so far: on a short screen the box matters
	// more than the labels, so past two rows it drops to bare hotkeys.
	legendRows := lg.lines(sz.cols() - left)
	if maxLegend := max(1, min(2, sz.rows()-6)); len(legendRows) > maxLegend {
		legendRows = lg.keysOnly(sz.cols() - left)
	}
	lines = append(fit(lines, sz.rows()-3-len(legendRows)), "")
	lines = append(lines, legendRows...)
	var b strings.Builder
	b.WriteString("\x1b[H")
	b.WriteString("\r\n")
	for _, ln := range lines {
		b.WriteString(pref)
		b.WriteString(ln)
		b.WriteString("\x1b[K\r\n") // clear to end of line (erase stale tails)
	}
	b.WriteString("\x1b[J") // clear everything below the frame
	return b.String()
}

// fit clamps a framed body to n lines, keeping the top and the closing rows. The
// middle is what goes: a terminal too short for its content shows less content —
// never a box with no bottom. This is the backstop; each screen sizes its own
// variable-length part first, so in practice it only fires on absurd geometry.
func fit(lines []string, n int) []string {
	n = max(n, 2)
	if len(lines) <= n {
		return lines
	}
	keep := min(2, n-1) // the status row and the bottom border
	out := make([]string, 0, n)
	out = append(out, lines[:n-keep]...)
	return append(out, lines[len(lines)-keep:]...)
}

// legend is the key legend shown under the box, kept as key/label pairs so it can
// be laid out against whatever width the terminal actually has.
type legend [][2]string

func legendLine(pairs [][2]string) legend { return legend(pairs) }

// lines lays the legend out in width columns. It is the one thing drawn outside
// the box, so the frame's own width clamp doesn't cover it — and a legend that
// runs past the edge wraps, costing an unaccounted screen row and pushing the
// next repaint out of alignment. It wraps on purpose instead, degrading to bare
// keys when even one labelled key won't fit.
func (l legend) lines(width int) []string {
	if width <= 4 {
		return nil
	}
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, p := range l {
		n := runeLen(p[0]) + 1 + runeLen(p[1]) + 3
		if n > width-2 {
			return l.keysOnly(width)
		}
		if curLen == 0 {
			cur.WriteString("  ")
			curLen = 2
		} else if curLen+n > width {
			out = append(out, cur.String())
			cur.Reset()
			cur.WriteString("  ")
			curLen = 2
		}
		cur.WriteString(paint(p[0], inkBlue)) // neon-cyan keys
		cur.WriteString(paint(" "+p[1]+"   ", inkDim))
		curLen += n
	}
	if curLen > 0 {
		out = append(out, cur.String())
	}
	return out
}

// keysOnly is the narrowest legend: the hotkeys with no labels, cut to fit.
func (l legend) keysOnly(width int) []string {
	var b strings.Builder
	b.WriteString("  ")
	n := 2
	for _, p := range l {
		if p[0] == "" {
			continue
		}
		if n+runeLen(p[0])+1 > width {
			break
		}
		b.WriteString(paint(p[0], inkBlue) + " ")
		n += runeLen(p[0]) + 1
	}
	return []string{b.String()}
}

// Render returns the launcher frame: one line per profile — a cursor mark, the
// provider·account, and a quiet login/status on the right. When setupNeeded is
// true (the shortcut commands aren't installed) it shows a one-line nudge toward
// `s`. Pure, for tests.
func Render(rows []Row, cursor int, setupNeeded bool, sz Size) string {
	return renderFrame(rows, cursor, setupNeeded, 0, sz)
}

// renderFrame is Render with an animation phase (colour sweep offset). Static
// callers pass 0; the live loop advances it for the shimmering logo/border.
func renderFrame(rows []Row, cursor int, setupNeeded bool, phase int, sz Size) string {
	return renderPicker(rows, cursor, setupNeeded, phase, "", sz)
}

// renderPicker is renderFrame with the aiacc version shown in the status bar.
func renderPicker(rows []Row, cursor int, setupNeeded bool, phase int, version string, sz Size) string {
	w := innerWidth(sz.cols())
	nameCol := min(max(w/2, 10), 28) // the name column tracks the frame width

	lines := []string{boxTop("aiacc", w), boxBlank(w)}

	// Header: AIACC wordmark (gradient, with a periodic glitch burst) + tagline.
	// The wordmark is a fixed block of columns and carries colour escapes that
	// render() cannot measure, so it is the one row that can't be clamped after
	// the fact. In a frame too narrow for it, a plain wordmark stands in.
	caret := " "
	if (phase/6)%2 == 0 {
		caret = "▊" // blinking block cursor
	}
	if w >= 3+runeLen(logoRows[0]) && sz.rows() >= 20 {
		for i, lg := range logoRows {
			lines = append(lines, boxRaw("   "+logoArt(lg, phase+i), 3+runeLen(lg), w))
		}
		lines = append(lines, boxRow([]seg{pad(3), {"> ", inkGreen}, {"launch a profile ", inkBlue}, {caret, inkGreen}}, w))
	} else {
		// Too narrow for the wordmark, or too short to spend three rows on it.
		lines = append(lines, boxRow([]seg{pad(2), {"AIACC ", inkGreen}, {"> ", inkGreen},
			{"launch a profile ", inkBlue}, {caret, inkGreen}}, w))
	}
	lines = append(lines, boxDivider(w), boxBlank(w))

	if setupNeeded {
		nudge := " to install the shortcut commands"
		switch {
		case w < 34:
			nudge = " → set up"
		case w < 46:
			nudge = " to install commands"
		}
		lines = append(lines,
			boxRow([]seg{pad(2), {"⚡ press ", inkYellow}, {"s", inkWhite}, {nudge, inkYellow}}, w),
			boxBlank(w),
		)
	}

	if len(rows) == 0 {
		lines = append(lines,
			boxRow([]seg{pad(2), {"No profiles yet.", inkWhite}}, w),
			boxRow([]seg{pad(2), {"Press ", inkGrey}, {"a", inkWhite}, {" to add one — settings: ", inkGrey}, {"glm", inkWhite}, {" for an API key.", inkGrey}}, w),
			boxBlank(w),
		)
	}

	// Vertical fit: the list scrolls inside the frame rather than pushing the
	// frame off the screen. Everything outside the list is fixed height, so the
	// budget is whatever the terminal has left after it.
	const fixedTail = 4 // blank + divider + status + bottom border
	budget := max(1, sz.rows()-len(lines)-fixedTail-5)
	start, end := 0, len(rows)
	if budget < len(rows) {
		start, end = window(len(rows), max(cursor, 0), max(1, budget-1)) // -1 for the "more" line
	}

	for i, r := range rows[start:end] {
		i += start
		focused := i == cursor
		blocked := !r.launchable()

		name := r.Account
		if r.Provider != "claude" {
			name += " (" + r.Provider + ")"
		}
		// Settings tag: the file's basename only — the path is data aiacc already
		// owns. Only the edit action reads the file, and never shows its key.
		tag := ""
		if r.Settings != "" {
			tag = " ⚙ " + filepath.Base(r.Settings)
		}
		warn := ""
		if blocked {
			warn = "⚠ "
		}

		var infoText string
		infoInk := inkGrey
		switch {
		case blocked:
			infoText, infoInk = r.blockedReason(), inkRed
		case r.Email == "":
			infoText, infoInk = "not logged in", inkYellow
		default:
			infoText = trunc(r.Email, w-nameCol-4)
		}

		if focused {
			// Selection glow: the whole row reversed, prompt cursor inline.
			gap := max(1, nameCol-runeLen(warn)-runeLen(name)-runeLen(tag))
			text := "> " + warn + name + tag + strings.Repeat(" ", gap) + infoText
			lines = append(lines, boxRowHi(text, w))
			continue
		}
		nameInk := inkGrey
		if blocked {
			nameInk = inkRed
		}
		lines = append(lines, boxRow([]seg{
			pad(2),
			{warn, inkRed},
			{name, nameInk},
			{tag, inkDim},
			pad(nameCol - runeLen(warn) - runeLen(name) - runeLen(tag)),
			{infoText, infoInk},
		}, w))
	}

	if start > 0 || end < len(rows) {
		lines = append(lines, boxRow(moreLine(start, end, len(rows)), w))
	}

	// Status bar — terminal readout.
	lines = append(lines, boxBlank(w), boxDivider(w))
	sep, wait := "  ::  ", "⚡ SETUP REQUIRED"
	if w < 40 { // the readout shortens rather than being cut off mid-word
		sep, wait = " :: ", "⚡ SETUP"
	}
	status := []seg{pad(2), {"» ", inkGreen}, {plural(len(rows), "profile"), inkGrey}, {sep, inkDim}}
	if setupNeeded {
		status = append(status, seg{wait, inkYellow})
	} else {
		status = append(status, seg{"✓ READY", inkGreen})
	}
	lines = append(lines, boxRow(withVersion(status, version, w), w), boxBottom(w))

	pairs := [][2]string{{"↑↓", "move"}, {"⏎", "launch"}, {"a", "add"}, {"r", "rename"}, {"c", "duplicate"}, {"e", "edit"}, {"h", "hand off"}, {"u", "resume"}, {"d", "remove"}}
	if setupNeeded {
		pairs = append(pairs, [2]string{"s", "setup"})
	}
	pairs = append(pairs, [2]string{"q", "quit"})
	return frame(lines, legendLine(pairs), sz, w)
}

// semverLike accepts a release version with or without its leading v. The
// build's -ldflags value is a bare "0.21.0"; the label always carries one v.
var semverLike = regexp.MustCompile(`^v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?)$`)

// versionLabel is the status-bar form of the build version: "0.21.0" and
// "v0.21.0" both read "v0.21.0"; "dev", an empty value, or anything that is not
// a release version reads "dev", so a local build never shows a blank or a bare v.
func versionLabel(v string) string {
	m := semverLike.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "dev"
	}
	return "v" + m[1]
}

// withVersion right-aligns the dim version label after the status readout, one
// cell in from the border. It is the first thing to go: when the readout leaves
// no room for the label with a gap, the status is returned untouched, so a
// narrow terminal loses the version and never a character of the readout.
func withVersion(status []seg, version string, w int) []seg {
	if strings.TrimSpace(version) == "" {
		return status
	}
	label := versionLabel(version)
	used := 0
	for _, s := range status {
		used += runeLen(s.text)
	}
	if used+1+runeLen(label)+1 > w {
		return status
	}
	return append(status, pad(w-used-runeLen(label)-1), seg{label, inkDim})
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// RenderRemove is the confirm screen for removing a profile. Removing only
// unregisters it from aiacc; the config dir is left on disk, so it is reversible
// by re-adding. It asks first — an explicit `y`, never Enter.
func RenderRemove(r Row, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{
		boxTop("aiacc — remove profile", w), boxBlank(w),
		boxRow([]seg{pad(2), {"Remove ", inkWhite}, {r.Account, inkYellow}, {" from aiacc?", inkWhite}}, w),
		boxBlank(w),
		boxRow([]seg{pad(2), {"The config dir is left on disk — only the", inkGrey}}, w),
		boxRow([]seg{pad(2), {"aiacc registration is removed.", inkGrey}}, w),
		boxBottom(w),
	}
	legend := legendLine([][2]string{{"y", "remove"}, {"n", "cancel"}})
	return frame(lines, legend, sz, w)
}

// --- Shell setup --------------------------------------------------------------

// SetupResult is what a one-step `aiacc setup` installed, for the result screen.
type SetupResult struct {
	BinDir    string   // display path the launchers were written to
	Names     []string // installed command names
	Example   string   // an example command to try
	WorksNow  bool     // BinDir is on PATH → usable immediately, no reload
	Shared    int      // profiles that now share your skills/agents/commands
	Conflicts int      // entries left alone because a profile owns them
	Skipped   []string // launcher paths left alone: a file aiacc did not write sits there
	Backups   []string // originals moved aside by --replace before the takeover
}

// RenderSetupResult returns the framed outcome of a one-step setup: what was
// installed and whether it works now. Pure, for tests.
func RenderSetupResult(r SetupResult, sz Size) string {
	w := innerWidth(sz.cols())
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
	if r.Shared > 0 {
		lines = append(lines, boxRow([]seg{pad(2), {"✓ ", inkGreen},
			{"skills · agents · commands shared with " + strconv.Itoa(r.Shared), inkWhite}}, w))
	}
	if r.Conflicts > 0 {
		lines = append(lines, boxRow([]seg{pad(4),
			{strconv.Itoa(r.Conflicts) + " left alone — aiacc link --replace", inkYellow}}, w))
	}
	for _, p := range r.Skipped {
		lines = append(lines, boxRow([]seg{pad(4),
			{p + " is not an aiacc launcher — left alone (setup --replace)", inkYellow}}, w))
	}
	for _, b := range r.Backups {
		lines = append(lines, boxRow([]seg{pad(4), {"original kept as " + b, inkDim}}, w))
	}
	if r.Shared > 0 || r.Conflicts > 0 || len(r.Skipped) > 0 || len(r.Backups) > 0 {
		lines = append(lines, boxBlank(w))
	}
	if r.WorksNow {
		lines = append(lines, boxRow([]seg{pad(2), {"They work now — try: ", inkWhite}, {r.Example, inkBlue}}, w))
	} else {
		lines = append(lines, boxRow([]seg{pad(2), {"Open a new terminal, then: ", inkWhite}, {r.Example, inkBlue}}, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"q", "done"}}), sz, w)
}

// driveSetupResult shows the result and waits for an exit key.
func driveSetupResult(r SetupResult, g *geom, in io.Reader, out io.Writer) error {
	return screenLoop(g, in, out, readKey, func(sz Size) string { return RenderSetupResult(r, sz) },
		func(k key) bool { return k == keyQuit || k == keyEnter || k == keyCopy })
}

// RunSetupResult shows the setup result screen on /dev/tty.
func RunSetupResult(r SetupResult) error {
	return onTTYErr(func(tty *os.File, g *geom) error { return driveSetupResult(r, g, tty, tty) })
}

// --- Add profile --------------------------------------------------------------

// AddResult is the outcome of the framed add screen. OK is false when cancelled.
type AddResult struct {
	Name     string
	Dir      string
	Settings string // optional extra settings file; "" = none
	OK       bool
}

// AddForm is the add screen's state. Field indexes the focused input: 0 name,
// 1 dir, 2 settings. WithSettings shows the settings input, for a provider
// whose CLI can load an extra settings file.
type AddForm struct {
	Name, Dir, Settings string
	Field               int
	WithSettings        bool
}

func (f AddForm) fields() int {
	if f.WithSettings {
		return 3
	}
	return 2
}

// ValidName reports whether s is a legal profile name: a letter/underscore, then
// letters, digits, hyphens, or underscores. The name is also the launcher
// command, so it must be a legal shell function name — which is why a name like
// "??????????" cannot be entered. The rule is enforced at registration on every
// surface — here on the picker as you type, and by saveAccount/renameAccount on
// the CLI. A hand-edited config.toml is read as-is, though; doctor is the
// backstop for anything that got in that way.
func ValidName(s string) bool {
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

func defaultDir(provider, name string) string {
	p := provider
	if p == "" {
		p = "profile"
	}
	if name == "" {
		return "~/." + p + "-<name>"
	}
	return "~/." + p + "-" + name
}

// RenderAdd returns the add-profile frame: the target provider, a live name
// field, a dir field that defaults to ~/.<provider>-<name> until edited, an
// optional settings field when the provider supports one, the current login for
// context, and an optional hint. Pure, for tests.
func RenderAdd(currentLogin, provider string, f AddForm, hint string, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{boxTop("aiacc — add profile", w), boxBlank(w)}

	lines = append(lines, boxRow([]seg{pad(2), {"provider  ", inkGrey}, {provider, inkBlue}}, w))
	if currentLogin != "" {
		lines = append(lines,
			boxRow([]seg{pad(2), {"current   ", inkGrey}, {trunc(currentLogin, w-12), inkDim}}, w),
		)
	}
	lines = append(lines, boxBlank(w))

	// input renders one field: label, value (or a dim placeholder), and the
	// caret when focused.
	input := func(i int, label, value, placeholder string) string {
		labelInk, caret := inkGrey, ""
		if f.Field == i {
			labelInk, caret = inkWhite, "▏"
		}
		v := seg{value, inkWhite}
		if value == "" {
			v = seg{placeholder, inkDim}
		}
		return boxRow([]seg{pad(2), {label, labelInk}, v, {caret, inkPink}}, w)
	}
	lines = append(lines,
		input(0, "name      ", f.Name, ""),
		input(1, "dir       ", f.Dir, defaultDir(provider, f.Name)),
	)
	if f.WithSettings {
		lines = append(lines, input(2, "settings  ", f.Settings, "glm · custom — or a path"))
	}

	lines = append(lines, boxBlank(w), hintRow(hint, w), boxBottom(w))

	// The name becomes the launcher command, so spell that out.
	tip := "type name"
	if ValidName(f.Name) {
		tip = "launches as: " + f.Name
	}
	legend := legendLine([][2]string{{"", tip}, {"⇥", "field"}, {"⏎", "create"}, {"esc", "cancel"}})
	return frame(lines, legend, sz, w)
}

// driveAdd is the add-screen input loop, decoupled from /dev/tty for tests. It
// filters keystrokes so the name field can only ever hold command-safe chars.
func driveAdd(currentLogin, providerName string, withSettings bool, g *geom, in io.Reader, out io.Writer) (AddResult, error) {
	f, hint := AddForm{WithSettings: withSettings}, ""
	var result AddResult
	err := screenLoop(g, in, out, (*bufio.Reader).ReadByte,
		func(sz Size) string { return RenderAdd(currentLogin, providerName, f, hint, sz) },
		func(b byte) bool {
			// focused is the text the keystroke edits; the name field is the only
			// one with a narrower alphabet.
			focused := []*string{&f.Name, &f.Dir, &f.Settings}[f.Field]
			switch b {
			case 0x1b, 3: // Esc / Ctrl-C
				return true
			case '\t':
				f.Field, hint = (f.Field+1)%f.fields(), ""
			case '\r', '\n':
				if !ValidName(f.Name) {
					f.Field, hint = 0, "name: a letter first, then letters, digits, - or _"
					return false
				}
				d := f.Dir
				if d == "" {
					d = defaultDir(providerName, f.Name)
				}
				settings, err := resolveSettings(providerName, d, f.Settings)
				if err != nil {
					hint = err.Error() // e.g. an existing target — nothing was overwritten
					return false
				}
				result = AddResult{Name: f.Name, Dir: d, Settings: settings, OK: true}
				return true
			case 0x7f, 0x08: // Backspace / Delete
				*focused, hint = trimLastByte(*focused), ""
			default:
				if (f.Field == 0 && nameByte(b)) || (f.Field > 0 && printableByte(b)) {
					*focused += string(b)
				}
				hint = ""
			}
			return false
		})
	return result, err
}
func trimLastByte(s string) string {
	if s == "" {
		return s
	}
	return s[:len(s)-1]
}

// resolveSettings turns the settings field's value into the path the profile
// registers, through the one classification rule the CLI's --settings flag
// shares (provider.ClassifySettings): an endpoint keyword (glm, custom)
// generates the settings skeleton into the profile dir and yields the
// generated path, exactly as if the user had written the file by hand and
// passed its path; a bare word that names no keyword is refused with the
// valid ones listed, and the hint row keeps that refusal on screen; anything
// path-like is taken verbatim as before. A file already at the target aborts
// the create with its path — nothing is overwritten (the aiacc link rule),
// and the caller stays on the screen.
func resolveSettings(providerName, dir, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	ep, keyword, err := provider.ClassifySettings(providerName, value)
	if err != nil {
		return "", err // names the typo and the valid keywords
	}
	if !keyword {
		// Today's path behavior, unchanged, except a directory is refused.
		if err := provider.RefuseSettingsDir(value); err != nil {
			return "", err
		}
		return value, nil
	}
	path, err := provider.EnsureSkeleton(dir, ep)
	if err != nil {
		// The hint row truncates at the inner width minus 4 columns (the
		// padding and the ⚠ marker), so the file name leads the message and
		// its glyph survives any width; the dir follows it (and also sits in
		// the dir field right above the hint).
		if errors.Is(err, provider.ErrSettingsExists) {
			return "", fmt.Errorf("%s already exists in %s — remove it or pick another dir", ep.Filename, dir)
		}
		return "", err
	}
	return path, nil
}

// --- Rename profile -----------------------------------------------------------

// RenderRename returns the rename screen: a single name field prefilled with the
// current name, plus a hint. Pure, for tests.
func RenderRename(oldName, buf, hint string, sz Size) string {
	w := innerWidth(sz.cols())
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
	return frame(lines, legendLine([][2]string{{"", "edit name"}, {"⏎", "rename"}, {"esc", "cancel"}}), sz, w)
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
func driveRename(oldName string, taken map[string]bool, g *geom, in io.Reader, out io.Writer) (string, error) {
	buf, hint := oldName, ""
	var chosen string
	err := screenLoop(g, in, out, (*bufio.Reader).ReadByte,
		func(sz Size) string { return RenderRename(oldName, buf, hint, sz) },
		func(b byte) bool {
			switch b {
			case 0x1b, 3: // Esc / Ctrl-C
				return true
			case '\r', '\n':
				switch {
				case buf == oldName:
					return true // unchanged — nothing to do
				case !ValidName(buf):
					hint = "name: a letter first, then letters, digits, - or _"
				case taken[buf]:
					hint = "a profile named " + buf + " already exists"
				default:
					chosen = buf
					return true
				}
			case 0x7f, 0x08:
				buf, hint = trimLastByte(buf), ""
			default:
				if nameByte(b) {
					buf += string(b)
				}
				hint = ""
			}
			return false
		})
	return chosen, err
}

// --- Generic list + message screens ------------------------------------------

// ListItem is one row of a generic selection list.
type ListItem struct {
	Primary   string // the main label
	Secondary string // a dim detail on the right (optional)
}

// RenderList returns a framed single-column selection list. Pure, for tests.
func RenderList(title string, items []ListItem, cursor int, sz Size) string {
	w := innerWidth(sz.cols())
	primCol := min(max(w/2, 8), 24)
	lines := []string{boxTop(title, w), boxBlank(w)}
	if len(items) == 0 {
		lines = append(lines, boxRow([]seg{pad(2), {"nothing to choose", inkDim}}, w), boxBlank(w))
	}
	budget := max(1, sz.rows()-len(lines)-1-5) // 1 bottom border + frame chrome
	start, end := 0, len(items)
	if budget < len(items) {
		start, end = window(len(items), max(cursor, 0), max(1, budget-1))
	}
	for i, it := range items[start:end] {
		i += start
		focused := i == cursor
		cur := seg{"  ", ""}
		if focused {
			cur = seg{"> ", inkGreen}
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
	if start > 0 || end < len(items) {
		lines = append(lines, boxRow(moreLine(start, end, len(items)), w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine([][2]string{{"↑↓", "move"}, {"⏎", "select"}, {"q", "cancel"}}), sz, w)
}

// driveList is the selection loop, decoupled from /dev/tty for tests. Returns the
// chosen index, or -1 if cancelled.
func driveList(title string, items []ListItem, g *geom, in io.Reader, out io.Writer) (int, error) {
	cursor, chosen := 0, -1
	if len(items) == 0 {
		cursor = -1
	}
	err := screenLoop(g, in, out, readKey, func(sz Size) string { return RenderList(title, items, cursor, sz) },
		func(k key) bool {
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
					chosen = cursor
					return true
				}
			case keyQuit, keyCopy:
				return true
			}
			return false
		})
	return chosen, err
}

// RunList shows a selection list on /dev/tty and returns the chosen index or -1.
func RunList(title string, items []ListItem) (int, error) {
	return onTTY(-1, func(tty *os.File, g *geom) (int, error) { return driveList(title, items, g, tty, tty) })
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
func RenderMessage(title string, body []Line, sz Size) string {
	return renderMessage(title, body, Line{}, sz)
}

// renderMessage is RenderMessage with a transient status note under the body —
// the confirmation that Ctrl-C put the commands on the clipboard, or the reason
// it couldn't. Empty note renders the plain screen.
func renderMessage(title string, body []Line, note Line, sz Size) string {
	return renderCheckMessage(title, body, note, nil, false, sz)
}

// Checkbox is an optional toggle on a message screen: a row under the body that
// space or b flips. While it is checked, Warning is drawn under it.
type Checkbox struct {
	Label   string // the row text after the box, e.g. "bypass permissions (--x)"
	Warning string // one line, shown only while checked
}

// renderCheckMessage is renderMessage with an optional checkbox between the body
// and the status note. box nil renders the plain screen.
func renderCheckMessage(title string, body []Line, note Line, box *Checkbox, checked bool, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{boxTop(title, w), boxBlank(w)}
	for _, l := range body {
		lines = append(lines, boxRow([]seg{pad(2), {l.Text, l.Color}}, w))
	}
	keys := [][2]string{{"q", "close"}}
	if copyText(body) != "" {
		keys = [][2]string{{"^C", "copy"}, {"q", "close"}}
	}
	if box != nil {
		mark := "[ ] "
		if checked {
			mark = "[x] "
		}
		lines = append(lines, boxBlank(w), boxRow([]seg{pad(2), {trunc(mark+box.Label, w-4), inkWhite}}, w))
		if checked {
			lines = append(lines, boxRow([]seg{pad(6), {trunc(box.Warning, w-8), inkYellow}}, w))
		}
		keys = append([][2]string{{"space/b", "bypass"}}, keys...)
	}
	if note.Text != "" {
		lines = append(lines, boxBlank(w), boxRow([]seg{pad(2), {trunc(note.Text, w-4), note.Color}}, w))
	}
	lines = append(lines, boxBottom(w))
	return frame(lines, legendLine(keys), sz, w)
}

// RunMessage shows a framed message on /dev/tty until an exit key.
func RunMessage(title string, body []Line) error {
	return runMessage(title, func(bool) []Line { return body }, nil)
}

// RunMessageCheck is RunMessage with a checkbox (default unchecked). body is
// rebuilt for the box's state on every repaint, so the lines shown and the lines
// ^C copies always agree.
func RunMessageCheck(title string, box Checkbox, body func(checked bool) []Line) error {
	return runMessage(title, body, &box)
}

// runMessage is the one message path: both entry points open the terminal and
// run driveMessage on it.
func runMessage(title string, body func(checked bool) []Line, box *Checkbox) error {
	return onTTYErr(func(tty *os.File, g *geom) error {
		return driveMessage(title, body, box, g, tty, tty, clipCopy)
	})
}

// driveMessage runs a message screen until an exit key, decoupled from /dev/tty
// for tests. box nil means no checkbox, and space and b do nothing. clip is the
// clipboard writer, reporting whether it succeeded.
func driveMessage(title string, body func(checked bool) []Line, box *Checkbox, g *geom, in io.Reader, out io.Writer, clip func(string) bool) error {
	var note Line
	checked := false
	var lines []Line // the body as last drawn, so ^C copies what is on screen
	return screenLoop(g, in, out, readKey,
		func(sz Size) string {
			lines = body(checked)
			return renderCheckMessage(title, lines, note, box, checked, sz)
		},
		func(k key) bool {
			switch k {
			case keyQuit, keyEnter:
				return true
			case keyToggle:
				if box != nil {
					checked = !checked
					note = Line{}
				}
			case keyCopy:
				// Ctrl-C on a screen whose whole point is a command you're about to
				// run: copy it rather than close, which is what the key means
				// everywhere else text is on screen. `q` still closes.
				text := copyText(lines)
				switch {
				case text == "":
					return true // nothing to copy — fall back to closing
				case clip(text):
					note = Line{Text: "✓ copied to clipboard", Color: Green}
				default:
					note = Line{Text: "no clipboard tool (pbcopy/wl-copy/xclip/xsel)", Color: Red}
				}
			}
			return false
		})
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
	keyDuplicate
	keyEdit
	keyHandoff
	keyResume
	keyRemove
	keySetup
	keyCopy
	keyToggle
	keyYes
	keyNo
	keyQuit
)

// geom is the terminal's live geometry. A terminal can be resized at any moment,
// so the size is not read once and trusted: SIGWINCH re-measures it and every
// repaint reads it back through get(). Guarded because the signal watcher and the
// render loop are different goroutines.
type geom struct {
	mu      sync.Mutex
	sz      Size
	resized bool
}

func (g *geom) get() Size {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sz
}

// set records a new size and flags that the screen changed shape.
func (g *geom) set(sz Size) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if sz.Cols > 0 && sz != g.sz {
		g.sz, g.resized = sz, true
	}
}

// takeResized reports (and clears) whether the terminal changed shape since the
// last repaint. A shrunk terminal leaves wrapped debris the in-place repaint
// can't reach, so the caller does one full erase when this is true.
func (g *geom) takeResized() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	was := g.resized
	g.resized = false
	return was
}

// fixedGeom is a geom that never changes — for tests and non-tty renders.
func fixedGeom(cols, rows int) *geom { return &geom{sz: Size{Cols: cols, Rows: rows}} }

// openRawTTY puts /dev/tty in raw mode and returns it, its live geometry, and a
// restore func. A signal handler restores the tty and shows the cursor before
// exiting on SIGTERM/HUP (or a SIGINT that slips past -isig), so no exit path
// leaves the user in a hidden-cursor raw terminal. A second handler keeps the
// geometry current on SIGWINCH.
func openRawTTY() (*os.File, *geom, func(), error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	saved, err := sttyState(tty)
	if err != nil {
		tty.Close()
		return nil, nil, nil, err
	}
	// The resize watcher is torn down with the terminal: a session opens one of
	// these per screen (picker → add → picker → …), and a watcher left running on
	// a closed tty would re-measure a file that is gone on every later resize.
	winch := make(chan os.Signal, 1)
	var once sync.Once
	restore := func() {
		once.Do(func() {
			signal.Stop(winch)
			close(winch)
			stty(tty, saved)
			fmt.Fprint(tty, showCursor+clearHome)
			tty.Close()
		})
	}
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	// ponytail: this goroutine parks until a signal or process exit; a CLI runs a
	// couple of these at most, so the parked goroutine is not worth a teardown.
	go exitOnSignal(sigc, restore, os.Exit)
	if err := stty(tty, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		restore()
		return nil, nil, nil, err
	}
	fmt.Fprint(tty, hideCursor)

	g := &geom{sz: ttySize(tty)}
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			g.set(ttySize(tty))
		}
	}()
	return tty, g, restore, nil
}

// repaint returns the escape prefix for the next frame: normally nothing (the
// frame repaints in place), but a full erase right after a resize, because a
// terminal that just shrank holds wrapped debris outside the new frame's reach.
func repaint(g *geom) string {
	if g.takeResized() {
		return clearHome
	}
	return ""
}

// openTerminal is openRawTTY behind a seam, so a test can watch the restore.
var openTerminal = openRawTTY

// onTTY runs fn on /dev/tty in raw mode and restores the terminal on every
// return path: normal exit, a key-driven exit, or an error. zero is what the
// caller gets back when the terminal cannot be opened.
func onTTY[T any](zero T, fn func(tty *os.File, g *geom) (T, error)) (T, error) {
	tty, g, restore, err := openTerminal()
	if err != nil {
		return zero, err
	}
	defer restore()
	return fn(tty, g)
}

// onTTYErr is onTTY for a screen with no result.
func onTTYErr(fn func(tty *os.File, g *geom) error) error {
	_, err := onTTY(struct{}{}, func(tty *os.File, g *geom) (struct{}, error) {
		return struct{}{}, fn(tty, g)
	})
	return err
}

// screenLoop is the loop every framed screen shares: repaint, read one input
// (a key, or a raw byte for the text forms), hand it to handle. frame renders the
// screen at the live geometry, and handle reports true once the screen is done.
// End of input ends the loop quietly; any other read error is returned.
func screenLoop[K any](g *geom, in io.Reader, out io.Writer, read func(*bufio.Reader) (K, error), frame func(Size) string, handle func(K) bool) error {
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, repaint(g)+frame(g.get()))
		k, err := read(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if handle(k) {
			return nil
		}
	}
}

// Run shows the profile launcher on /dev/tty and returns the user's Result.
// setupNeeded surfaces the "install the shortcut commands" nudge. version is the
// build version shown in the status bar ("" shows nothing).
func Run(rows []Row, setupNeeded bool, version string) (Result, error) {
	return onTTY(Result{Kind: Cancelled}, func(tty *os.File, g *geom) (Result, error) {
		return animate(rows, setupNeeded, version, g, tty)
	})
}

// animate is the live picker loop: it reads keys in a goroutine and repaints on a
// ticker so the logo shimmers. Shares the key-state machine with drive().
func animate(rows []Row, setupNeeded bool, version string, g *geom, tty *os.File) (Result, error) {
	st := pickerState{cursor: initialCursor(rows), confirm: -1, version: version}
	keys := make(chan key)
	done := make(chan struct{})
	defer close(done)
	go func() {
		r := bufio.NewReader(tty)
		for {
			k, err := readKey(r)
			if err != nil {
				return
			}
			select {
			case keys <- k:
			case <-done:
				return
			}
		}
	}()

	ticker := time.NewTicker(90 * time.Millisecond)
	defer ticker.Stop()
	phase := 0
	for {
		sz := g.get()
		fmt.Fprint(tty, repaint(g))
		fmt.Fprint(tty, st.frame(rows, setupNeeded, phase, sz))
		select {
		case k := <-keys:
			if res, done := st.handle(rows, k); done {
				return res, nil
			}
		case <-ticker.C:
			phase++
		}
	}
}

// RunAdd shows the framed add screen on /dev/tty for the given provider.
// currentLogin (may be "") is shown for context; withSettings adds the optional
// settings-file field.
func RunAdd(currentLogin, provider string, withSettings bool) (AddResult, error) {
	return onTTY(AddResult{}, func(tty *os.File, g *geom) (AddResult, error) {
		return driveAdd(currentLogin, provider, withSettings, g, tty, tty)
	})
}

// RunRename shows the rename screen on /dev/tty. taken is the set of other
// account names the new one may not collide with. Returns the new name, or "" if
// cancelled/unchanged.
func RunRename(oldName string, taken []string) (string, error) {
	set := make(map[string]bool, len(taken))
	for _, n := range taken {
		set[n] = true
	}
	return onTTY("", func(tty *os.File, g *geom) (string, error) {
		return driveRename(oldName, set, g, tty, tty)
	})
}

// pickerState is the cursor + remove-confirm state shared by the testable drive
// loop and the animated Run loop.
type pickerState struct {
	cursor  int
	confirm int    // >=0 while confirming removal of that row
	version string // shown in the status bar; "" shows nothing
}

// handle applies one keypress. It returns (result, true) when the picker is done,
// or (_, false) to keep looping. The cursor visits every row so any profile can
// be removed, but Enter only launches a launchable one — the guard is on the
// action, not the cursor. Removal needs an explicit y; Enter is inert there.
func (s *pickerState) handle(rows []Row, k key) (Result, bool) {
	if s.confirm >= 0 {
		switch k {
		case keyYes:
			return Result{Kind: Remove, Index: s.confirm}, true
		case keyNo, keyQuit, keyCopy:
			s.confirm = -1
		}
		return Result{}, false
	}
	switch k {
	case keyUp:
		s.cursor = step(rows, s.cursor, -1)
	case keyDown:
		s.cursor = step(rows, s.cursor, +1)
	case keyEnter:
		if s.cursor >= 0 && rows[s.cursor].launchable() {
			return Result{Kind: Launch, Index: s.cursor}, true
		}
	case keyRemove:
		if s.cursor >= 0 {
			s.confirm = s.cursor
		}
	case keyAdd:
		return Result{Kind: Add}, true
	case keyRename:
		if s.cursor >= 0 {
			return Result{Kind: Rename, Index: s.cursor}, true
		}
	case keyDuplicate:
		if s.cursor >= 0 {
			return Result{Kind: Duplicate, Index: s.cursor}, true
		}
	case keyEdit:
		if s.cursor >= 0 {
			return Result{Kind: Edit, Index: s.cursor}, true
		}
	case keyHandoff:
		if s.cursor >= 0 {
			return Result{Kind: Handoff, Index: s.cursor}, true
		}
	case keyResume:
		if s.cursor >= 0 {
			return Result{Kind: Resume, Index: s.cursor}, true
		}
	case keySetup:
		return Result{Kind: Setup}, true
	case keyQuit, keyCopy: // nothing to copy here — Ctrl-C keeps its usual meaning
		return Result{Kind: Cancelled}, true
	}
	return Result{}, false
}

// frame draws the picker as it stands: the remove confirm while one is pending,
// the list otherwise. Both loops draw through it.
func (s *pickerState) frame(rows []Row, setupNeeded bool, phase int, sz Size) string {
	if s.confirm >= 0 {
		return RenderRemove(rows[s.confirm], sz)
	}
	return renderPicker(rows, s.cursor, setupNeeded, phase, s.version, sz)
}

// drive is the non-animated launcher loop, decoupled from /dev/tty for tests.
func drive(rows []Row, setupNeeded bool, g *geom, in io.Reader, out io.Writer) (Result, error) {
	st := pickerState{cursor: initialCursor(rows), confirm: -1}
	res := Result{Kind: Cancelled}
	err := screenLoop(g, in, out, readKey, func(sz Size) string { return st.frame(rows, setupNeeded, 0, sz) },
		func(k key) bool {
			r, done := st.handle(rows, k)
			if done {
				res = r
			}
			return done
		})
	return res, err
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
	case 'q', 'Q':
		return keyQuit, nil
	case 3: // Ctrl-C, delivered as a byte under -isig. Screens that have
		// something worth copying take it; everywhere else it still quits.
		return keyCopy, nil
	case ' ', 'b', 'B':
		return keyToggle, nil
	case 'a', 'A':
		return keyAdd, nil
	case 'r', 'R':
		return keyRename, nil
	case 'c', 'C':
		return keyDuplicate, nil
	case 'e', 'E':
		return keyEdit, nil
	case 'h', 'H':
		return keyHandoff, nil
	case 'u', 'U':
		return keyResume, nil
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

// ttySize reads the terminal geometry via `stty size`, falling back to 80x24.
func ttySize(tty *os.File) Size {
	sz := Size{Cols: 80, Rows: 24}
	out, err := runStty(tty, "size")
	if err != nil {
		return sz
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return sz
	}
	if r, err := strconv.Atoi(f[0]); err == nil && r > 0 {
		sz.Rows = r
	}
	if c, err := strconv.Atoi(f[1]); err == nil && c > 0 {
		sz.Cols = c
	}
	return sz
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

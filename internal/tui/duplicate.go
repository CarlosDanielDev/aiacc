package tui

import (
	"bufio"
	"io"
	"os"
)

// --- Duplicate profile --------------------------------------------------------

// RenderDuplicate returns the duplicate screen: the source profile, a name
// field, and a dir field that defaults to ~/.<provider>-<name> until edited,
// plus the hint row where a refusal stays. Pure, for tests. f.WithSettings is
// ignored: the settings file is copied from the source, never typed.
func RenderDuplicate(provider, src string, f AddForm, hint string, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{
		boxTop("aiacc — duplicate profile", w), boxBlank(w),
		boxRow([]seg{pad(2), {"from      ", inkGrey}, {trunc(provider+"/"+src, w-12), inkDim}}, w),
		boxBlank(w),
	}
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
		boxBlank(w),
	)
	lines = append(lines, hintRows(hint, w)...)
	lines = append(lines, boxBottom(w))
	legend := legendLine([][2]string{{"", "settings copied, login not"}, {"⇥", "field"}, {"⏎", "duplicate"}, {"esc", "cancel"}})
	return frame(lines, legend, sz, w)
}

// driveDuplicate is the duplicate-screen loop, decoupled from /dev/tty for
// tests. Enter hands the typed name and dir ("" = the default) to submit, which
// does the whole duplicate or refuses it having written nothing; a refusal
// stays on the hint row and the screen stays open. Esc / Ctrl-C leave without
// calling submit. Returns true once submit has succeeded.
func driveDuplicate(provider, src string, submit func(name, dir string) error, g *geom, in io.Reader, out io.Writer) (bool, error) {
	f, hint, done := AddForm{}, "", false
	err := screenLoop(g, in, out, (*bufio.Reader).ReadByte,
		func(sz Size) string { return RenderDuplicate(provider, src, f, hint, sz) },
		func(b byte) bool {
			focused := []*string{&f.Name, &f.Dir}[f.Field]
			switch b {
			case 0x1b, 3: // Esc / Ctrl-C
				return true
			case '\t':
				f.Field, hint = (f.Field+1)%2, ""
			case '\r', '\n':
				if err := submit(f.Name, f.Dir); err != nil {
					hint = err.Error()
					return false
				}
				done = true
				return true
			case 0x7f, 0x08:
				*focused, hint = trimLastByte(*focused), ""
			default:
				if (f.Field == 0 && nameByte(b)) || (f.Field == 1 && printableByte(b)) {
					*focused += string(b)
				}
				hint = ""
			}
			return false
		})
	return done, err
}

// RunDuplicate shows the duplicate screen for provider/src on /dev/tty. submit
// is called on Enter (see driveDuplicate). Returns true once a duplicate was made.
func RunDuplicate(provider, src string, submit func(name, dir string) error) (bool, error) {
	return onTTY(false, func(tty *os.File, g *geom) (bool, error) {
		return driveDuplicate(provider, src, submit, g, tty, tty)
	})
}

package tui

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// --- Edit account settings ----------------------------------------------------

// EditForm is the edit screen's state. Field indexes the focused input: 0 token,
// 1 base URL, 2 model. SettingsEnv carries what the file holds (prefilled base URL
// and model, and whether a token is set). Token is what the user has typed; the
// file's token value is never read into the form.
type EditForm struct {
	provider.SettingsEnv
	Field int
	Token string
}

// editFields is the number of inputs the screen cycles through.
const editFields = 3

// editDotCap bounds the mask so a long token never widens the frame. The dots
// show that something is typed, not how long the key is.
const editDotCap = 24

// RenderEdit returns the edit-account frame: the account and its settings file's
// base name, the token field masked, the base URL and model prefilled, and an
// optional hint (a refusal from the save). Pure, for tests.
func RenderEdit(account, file, hint string, f EditForm, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{boxTop("aiacc — edit profile", w), boxBlank(w)}
	lines = append(lines,
		boxRow([]seg{pad(2), {"account   ", inkGrey}, {trunc(account, w-14), inkBlue}}, w),
		boxRow([]seg{pad(2), {"file      ", inkGrey}, {trunc(file, w-14), inkDim}}, w),
		boxBlank(w),
	)

	// row is one labelled input: the label and caret take the focus styling, and
	// value is the field's content (or its dim placeholder). idle is the caret's
	// style when unfocused; the token row has always carried none.
	row := func(i int, label string, value seg, idle seg) string {
		labelInk, caret := inkGrey, idle
		if f.Field == i {
			labelInk, caret = inkWhite, seg{"▏", inkPink}
		}
		return boxRow([]seg{pad(2), {label, labelInk}, value, caret}, w)
	}
	text := func(value string, placeholder seg) seg {
		if value == "" {
			return placeholder
		}
		return seg{trunc(value, w-16), inkWhite}
	}

	// The token is dots, one per byte typed, capped. An empty field says whether
	// the file already holds a key; it never looks prefilled.
	tokenView := seg{strings.Repeat("•", min(len(f.Token), editDotCap)), inkWhite}
	if f.Token == "" {
		tokenView = seg{"paste a new key", inkDim}
		if f.HasToken {
			tokenView = seg{"keep current key", inkDim}
		}
	}
	lines = append(lines,
		row(0, "token     ", tokenView, seg{"", ""}),
		row(1, "base url ", text(f.BaseURL, seg{"not set", inkDim}), seg{"", inkPink}),
		row(2, "model    ", text(f.Model, seg{"not set", inkDim}), seg{"", inkPink}),
		boxBlank(w),
	)

	lines = append(lines, hintRow(hint, w), boxBottom(w))
	legend := legendLine([][2]string{{"", "edit settings"}, {"⇥", "field"}, {"⏎", "save"}, {"esc", "cancel"}})
	return frame(lines, legend, sz, w)
}

// driveEdit is the edit-screen input loop, decoupled from /dev/tty for tests. It
// saves through provider.EditSettings on enter. A refused save leaves the file
// untouched and keeps the screen open with the reason. It reports whether the
// file was saved.
func driveEdit(path, account string, f EditForm, g *geom, in io.Reader, out io.Writer) (bool, error) {
	file := filepath.Base(path)
	hint := ""
	saved := false
	err := screenLoop(g, in, out, (*bufio.Reader).ReadByte,
		func(sz Size) string { return RenderEdit(account, file, hint, f, sz) },
		func(b byte) bool {
			focused := []*string{&f.Token, &f.BaseURL, &f.Model}[f.Field]
			switch b {
			case 0x1b, 3: // Esc / Ctrl-C
				return true
			case '\t':
				f.Field, hint = (f.Field+1)%editFields, ""
			case '\r', '\n':
				// An empty field keeps what the file has, so the save can't clear a key.
				err := provider.EditSettings(path, provider.SettingsEdit{
					Token:   f.Token,
					BaseURL: f.BaseURL,
					Model:   f.Model,
				})
				if err != nil {
					hint = err.Error() + " — nothing written"
					return false
				}
				saved = true
				return true
			case 0x7f, 0x08: // Backspace / Delete
				*focused, hint = trimLastByte(*focused), ""
			default:
				if printableByte(b) {
					*focused += string(b)
				}
				hint = ""
			}
			return false
		})
	return saved, err
}

// RunEdit shows the edit-account screen on /dev/tty for the settings file at
// path. It reports whether the file was saved.
func RunEdit(account, path string, f EditForm) (bool, error) {
	return onTTY(false, func(tty *os.File, g *geom) (bool, error) {
		return driveEdit(path, account, f, g, tty, tty)
	})
}

// exitOnSignal waits for the first shutdown signal, restores the terminal,
// aborts any settings write in flight (its temp file holds a new token, so it
// is removed), and exits. openRawTTY runs it; exit is a seam.
func exitOnSignal(sigc <-chan os.Signal, restore func(), exit func(int)) {
	<-sigc
	restore()
	provider.AbortSettingsWrites()
	exit(130)
}

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

// EditForm is the edit screen's state. Field indexes the focused row: 0 is the
// token, 1..len(Vals) the env values, and len(Vals)+1 the new-key row.
// SettingsEnv carries what the file holds (every editable env key and its
// value, and whether a token is set). Token is what the user has typed; the
// file's token value is never read into the form. AddKey is the name being
// typed on the new-key row.
type EditForm struct {
	provider.SettingsEnv
	Field  int
	Token  string
	AddKey string
}

// rows is the number of inputs the screen cycles through: the token, one per
// env key, and the new-key row.
func (f EditForm) rows() int { return len(f.Vals) + 2 }

// editDotCap bounds the mask so a long token never widens the frame. The dots
// show that something is typed, not how long the key is.
const editDotCap = 24

// focusedField is the string the focused row edits: the typed token, the
// focused env value, or the pending key name.
func focusedField(f *EditForm) *string {
	switch {
	case f.Field <= 0:
		return &f.Token
	case f.Field == len(f.Vals)+1:
		return &f.AddKey
	default:
		return &f.Vals[f.Field-1].Value
	}
}

// addKey places name in the form and returns the row to focus: the token row
// for the token's own name (its value is never a listed row), the existing row
// when the form already lists the key, or a new appended row.
func (f *EditForm) addKey(name string) int {
	if name == provider.EnvAuthToken {
		return 0
	}
	for i, kv := range f.Vals {
		if kv.Key == name {
			return i + 1
		}
	}
	f.Vals = append(f.Vals, provider.EnvVal{Key: name})
	return len(f.Vals)
}

// RenderEdit returns the edit-account frame: the account and its settings file's
// base name, the token field masked, one row per env key with its value, the
// new-key row, and an optional hint (a refusal from the save). Pure, for tests.
func RenderEdit(account, file, hint string, f EditForm, sz Size) string {
	w := innerWidth(sz.cols())
	lines := []string{boxTop("aiacc — edit profile", w), boxBlank(w)}
	lines = append(lines,
		boxRow([]seg{pad(2), {"account   ", inkGrey}, {trunc(account, w-14), inkBlue}}, w),
		boxRow([]seg{pad(2), {"file      ", inkGrey}, {trunc(file, w-14), inkDim}}, w),
		boxBlank(w),
	)

	// focused is whether row i holds the cursor. Field is clamped so a pure
	// render never highlights past the last row.
	focus := min(max(f.Field, 0), len(f.Vals)+1)
	// row is one labelled input: the label and caret take the focus styling, and
	// value is the field's content (or its dim placeholder). idle is the caret's
	// style when unfocused; the token row has always carried none.
	row := func(i int, label string, value seg, idle seg) string {
		labelInk, caret := inkGrey, idle
		if focus == i {
			labelInk, caret = inkWhite, seg{"▏", inkPink}
		}
		return boxRow([]seg{pad(2), {label, labelInk}, value, caret}, w)
	}
	// text clips a value to the width its label leaves over, so a long key
	// never pushes the value out of the row.
	text := func(label, value string, placeholder seg) seg {
		if value == "" {
			return placeholder
		}
		return seg{trunc(value, max(6, w-14-runeLen(label))), inkWhite}
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
	lines = append(lines, row(0, "token     ", tokenView, seg{"", ""}))

	// The env rows scroll inside the frame like the picker's list, so a file
	// with many keys still shows the focused row without tearing the box.
	start, end := 0, len(f.Vals)
	budget := max(1, sz.rows()-len(lines)-len(hintRows(hint, w))-7)
	if budget < len(f.Vals) {
		cursor := min(max(f.Field-1, 0), max(0, len(f.Vals)-1))
		start, end = window(len(f.Vals), cursor, max(1, budget-1)) // -1 for the "more" line
	}
	for i, kv := range f.Vals[start:end] {
		i += start
		lines = append(lines, row(i+1, kv.Key, text(kv.Key, kv.Value, seg{"not set", inkDim}), seg{"", inkPink}))
	}
	if start > 0 || end < len(f.Vals) {
		lines = append(lines, boxRow(moreLine(start, end, len(f.Vals)), w))
	}
	lines = append(lines,
		row(len(f.Vals)+1, "new key   ", text("new key", f.AddKey, seg{"type a key name", inkDim}), seg{"", inkPink}),
		boxBlank(w),
	)

	lines = append(lines, hintRows(hint, w)...)
	lines = append(lines, boxBottom(w))
	legend := legendLine([][2]string{{"", "edit settings"}, {"⇥", "field"}, {"⌃d", "drop key"}, {"⏎", "save"}, {"esc", "cancel"}})
	return frame(lines, legend, sz, w)
}

// driveEdit is the edit-screen input loop, decoupled from /dev/tty for tests.
// Enter saves through provider.EditSettings; on the new-key row it first adds
// the typed key (or jumps to the row of a key the form already has). Ctrl-D
// drops the focused env key. A refused save leaves the file untouched and
// keeps the screen open with the reason. It reports whether the file was saved.
func driveEdit(path, account string, f EditForm, g *geom, in io.Reader, out io.Writer) (bool, error) {
	file := filepath.Base(path)
	hint := ""
	saved := false
	err := screenLoop(g, in, out,
		func(r *bufio.Reader) (fieldKey, error) { return readFieldKey(r, in) },
		func(sz Size) string { return RenderEdit(account, file, hint, f, sz) },
		func(fk fieldKey) bool {
			switch fk.k {
			case keyQuit, keyCopy: // Esc / Ctrl-C
				return true
			case keyTab, keyDown:
				f.Field, hint = (f.Field+1)%f.rows(), ""
			case keyUp:
				f.Field, hint = (f.Field+f.rows()-1)%f.rows(), ""
			case keyDrop: // Ctrl-D drops the focused env key, never the token
				if f.Field > 0 && f.Field <= len(f.Vals) {
					f.Vals = append(f.Vals[:f.Field-1], f.Vals[f.Field:]...)
					// Land on an env row: typing after a drop edits a value,
					// never a stray new-key name.
					if f.Field > len(f.Vals) {
						f.Field = len(f.Vals)
					}
					hint = ""
				}
			case keyEnter:
				if f.Field == len(f.Vals)+1 && f.AddKey != "" {
					f.Field, f.AddKey, hint = f.addKey(f.AddKey), "", ""
					return false
				}
				// An empty token field keeps what the file has, so the save can't
				// clear a key.
				err := provider.EditSettings(path, provider.SettingsEdit{
					Token: f.Token,
					Vals:  f.Vals,
				})
				if err != nil {
					hint = err.Error() + " — nothing written"
					return false
				}
				saved = true
				return true
			case keyBackspace:
				*focusedField(&f), hint = trimLastByte(*focusedField(&f)), ""
			case keyText:
				*focusedField(&f) += string(fk.b)
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
		return driveEdit(path, account, f, g, &timedTTY{File: tty}, tty)
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

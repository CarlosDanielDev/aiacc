package tui

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// --- Edit account settings ----------------------------------------------------

// EditForm is the edit screen's state. Field indexes the focused input: 0 token,
// 1 base URL, 2 model. Token is what the user has typed, never a value read from
// the file: the file's token is only ever reported as HasToken. BaseURL and Model
// are prefilled from the file.
type EditForm struct {
	Field    int
	Token    string
	BaseURL  string
	Model    string
	HasToken bool
}

// fields is the number of inputs the screen cycles through.
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

	// input renders one field: label, value (or a dim placeholder), and the caret
	// when focused. The value is cut to the frame, never wider than it.
	input := func(i int, label, value string, placeholder seg) string {
		labelInk, caret := inkGrey, ""
		if f.Field == i {
			labelInk, caret = inkWhite, "▏"
		}
		v := seg{trunc(value, w-16), inkWhite}
		if value == "" {
			v = placeholder
		}
		return boxRow([]seg{pad(2), {label, labelInk}, v, {caret, inkPink}}, w)
	}

	// The token is dots, one per byte typed, capped. An empty field says whether
	// the file already holds a key; it never looks prefilled.
	tokenView := seg{strings.Repeat("•", min(len(f.Token), editDotCap)), inkWhite}
	tokenHolder := seg{"paste a new key", inkDim}
	if f.HasToken {
		tokenHolder = seg{"keep current key", inkDim}
	}
	if f.Token == "" {
		tokenView = tokenHolder
	}
	lines = append(lines,
		boxRow([]seg{pad(2), {"token     ", editLabelInk(f.Field, 0)}, tokenView, editCaret(f.Field, 0)}, w),
		input(1, "base url ", f.BaseURL, seg{"not set", inkDim}),
		input(2, "model    ", f.Model, seg{"not set", inkDim}),
		boxBlank(w),
	)

	if hint != "" {
		lines = append(lines, boxRow([]seg{pad(2), {"⚠ " + trunc(hint, w-4), inkRed}}, w))
	} else {
		lines = append(lines, boxBlank(w))
	}
	lines = append(lines, boxBottom(w))
	legend := legendLine([][2]string{{"", "edit settings"}, {"⇥", "field"}, {"⏎", "save"}, {"esc", "cancel"}})
	return frame(lines, legend, sz, w)
}

// editLabelInk and editCaret give the token row the same focus styling as input().
func editLabelInk(field, i int) string {
	if field == i {
		return inkWhite
	}
	return inkGrey
}

func editCaret(field, i int) seg {
	if field == i {
		return seg{"▏", inkPink}
	}
	return seg{"", ""}
}

// driveEdit is the edit-screen input loop, decoupled from /dev/tty for tests. It
// saves through provider.EditSettings on enter. A refused save leaves the file
// untouched and keeps the screen open with the reason. It reports whether the
// file was saved.
func driveEdit(path, account string, f EditForm, g *geom, in io.Reader, out io.Writer) (bool, error) {
	file := filepath.Base(path)
	hint := ""
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, repaint(g)+RenderEdit(account, file, hint, f, g.get()))
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				return false, nil
			}
			return false, err
		}
		focused := []*string{&f.Token, &f.BaseURL, &f.Model}[f.Field]
		switch b {
		case 0x1b, 3: // Esc / Ctrl-C
			return false, nil
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
				continue
			}
			return true, nil
		case 0x7f, 0x08: // Backspace / Delete
			*focused, hint = trimLastByte(*focused), ""
		default:
			if printableByte(b) {
				*focused += string(b)
			}
			hint = ""
		}
	}
}

// RunEdit shows the edit-account screen on /dev/tty for the settings file at
// path. It reports whether the file was saved.
func RunEdit(account, path string, f EditForm) (bool, error) {
	tty, g, restore, err := openRawTTY()
	if err != nil {
		return false, err
	}
	defer restore()
	return driveEdit(path, account, f, g, tty, tty)
}

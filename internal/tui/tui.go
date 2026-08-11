// Package tui renders the interactive account picker that `aiacc use` shows when
// no account is given. It draws on /dev/tty — stdout is consumed by the shell
// hook's command substitution — and returns the chosen row so the caller can
// emit the switch. Terminal raw mode is toggled with stty (POSIX; Linux/macOS,
// the release targets), so the package needs no non-stdlib dependency.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Row is one selectable account in the picker.
type Row struct {
	Provider string
	Account  string
	Email    string // logged-in account email, "" if not logged in
	Dir      string // expanded, ready for the export line
	Active   bool
	Tokens   int
}

const (
	clearScreen = "\x1b[2J\x1b[H"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
)

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyEnter
	keyQuit
)

// Run shows the picker on /dev/tty and returns the index of the chosen row, or
// -1 if the user cancelled (q / Ctrl-C).
func Run(rows []Row) (int, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return -1, err
	}
	defer tty.Close()

	saved, err := sttyState(tty)
	if err != nil {
		return -1, err
	}
	if err := stty(tty, "-icanon", "-echo", "min", "1", "time", "0"); err != nil {
		return -1, err
	}
	defer stty(tty, saved)
	defer fmt.Fprint(tty, showCursor+clearScreen)

	fmt.Fprint(tty, hideCursor)
	return Select(rows, tty, tty)
}

// Select runs the render/key loop against in/out. It is decoupled from /dev/tty
// so it can be driven with scripted keystrokes in tests. Returns the chosen row
// index, or -1 if cancelled.
func Select(rows []Row, in io.Reader, out io.Writer) (int, error) {
	if len(rows) == 0 {
		return -1, errors.New("no rows to select")
	}
	cursor := 0
	for i, r := range rows {
		if r.Active {
			cursor = i
			break
		}
	}
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, Render(rows, cursor))
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
			if cursor < len(rows)-1 {
				cursor++
			}
		case keyEnter:
			return cursor, nil
		case keyQuit:
			return -1, nil
		}
	}
}

// Render returns the full terminal frame for rows with the cursor on the given
// row. Lines are CRLF-terminated for raw mode.
func Render(rows []Row, cursor int) string {
	pw, aw, ew := len("PROVIDER"), len("ACCOUNT"), len("LOGIN")
	for _, r := range rows {
		if len(r.Provider) > pw {
			pw = len(r.Provider)
		}
		if len(r.Account) > aw {
			aw = len(r.Account)
		}
		if len(login(r)) > ew {
			ew = len(login(r))
		}
	}
	var b strings.Builder
	b.WriteString(clearScreen)
	b.WriteString("  Select an account  (↑/↓ move · enter switch · q cancel)\r\n\r\n")
	fmt.Fprintf(&b, "    %-*s  %-*s  %-*s  %-6s  %s\r\n", pw, "PROVIDER", aw, "ACCOUNT", ew, "LOGIN", "ACTIVE", "TOKENS")
	for i, r := range rows {
		cur := " "
		if i == cursor {
			cur = "▸"
		}
		active := ""
		if r.Active {
			active = "*"
		}
		fmt.Fprintf(&b, "  %s %-*s  %-*s  %-*s  %-6s  %s\r\n", cur, pw, r.Provider, aw, r.Account, ew, login(r), active, humanTokens(r.Tokens))
	}
	return b.String()
}

// login is the display value for the LOGIN column: the account email, or "-"
// when the directory has no logged-in account.
func login(r Row) string {
	if r.Email == "" {
		return "-"
	}
	return r.Email
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

// readKey decodes one logical key press: arrow keys (or vim j/k), enter, and
// quit (q / Ctrl-C / lone ESC).
func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyNone, err
	}
	switch b {
	case '\r', '\n':
		return keyEnter, nil
	case 'q', 'Q', 3: // 3 = Ctrl-C
		return keyQuit, nil
	case 'j':
		return keyDown, nil
	case 'k':
		return keyUp, nil
	case 0x1b: // ESC: start of an arrow sequence, or a lone ESC
		b2, err := r.ReadByte()
		if err != nil {
			return keyQuit, nil // lone ESC cancels
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

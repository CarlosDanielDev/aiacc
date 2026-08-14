// Package shell builds the shell-safe output that `aiacc use` emits for eval,
// plus the rc hooks installed by `aiacc shell-init` (ADR-0003).
//
// The export line is printed to stdout and eval'd by the user's shell, making
// it the primary shell-injection surface. Directory values and env-var names
// are therefore validated and single-quote escaped here before they ever reach
// a shell.
package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// ErrUnknownShell is returned for a shell aiacc does not support.
	ErrUnknownShell = errors.New("unknown shell")
	// ErrInvalidEnvVar is returned when the env-var name is not a valid shell
	// identifier.
	ErrInvalidEnvVar = errors.New("invalid env var name")
	// ErrUnsafeDir is returned when the directory is empty or contains a
	// control character that could corrupt or escape the emitted statement.
	ErrUnsafeDir = errors.New("unsafe directory value")
)

// envVarRe matches a POSIX shell identifier: a letter or underscore followed by
// letters, digits, or underscores.
var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ExportLine returns a single shell statement that points envVar at dir for the
// given shell. bash and zsh get `export NAME='dir'`; fish gets
// `set -gx NAME 'dir'`. dir is single-quote escaped so it cannot break out of
// the surrounding quotes.
//
// It errors if shellName is not bash, zsh, or fish; if envVar is not a valid
// shell identifier; or if dir is empty or holds a control character.
func ExportLine(shellName, envVar, dir string) (string, error) {
	if !envVarRe.MatchString(envVar) {
		return "", ErrInvalidEnvVar
	}
	if err := validateDir(dir); err != nil {
		return "", err
	}
	switch shellName {
	case "bash", "zsh":
		return fmt.Sprintf("export %s=%s", envVar, posixSingleQuote(dir)), nil
	case "fish":
		return fmt.Sprintf("set -gx %s %s", envVar, fishSingleQuote(dir)), nil
	default:
		return "", ErrUnknownShell
	}
}

// validateDir rejects empty input and any byte below 0x20 (NUL, tab, newline,
// carriage return, and every other ASCII control char), which are either unsafe
// for eval or garbage input.
func validateDir(dir string) error {
	if dir == "" {
		return ErrUnsafeDir
	}
	for i := 0; i < len(dir); i++ {
		if dir[i] < 0x20 {
			return ErrUnsafeDir
		}
	}
	return nil
}

// posixSingleQuote wraps s in single quotes for bash/zsh, replacing each embedded
// single quote with a close-quote, an escaped literal quote, then a reopen-quote.
// Backslashes are literal inside POSIX single quotes, so the quote is the only
// character that needs handling.
func posixSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishSingleQuote wraps s in single quotes for fish. Unlike POSIX shells, fish
// treats backslash as an escape inside single quotes, so backslashes must be
// doubled first; otherwise a value ending in a backslash could escape the quoting
// and inject fish commands. After that, single quotes are escaped as \'.
func fishSingleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "'", `\'`)
	return "'" + s + "'"
}

// RcLine is the line a user adds to their shell startup file to load the aiacc
// hook. bash/zsh eval the shell-init output; fish sources it.
func RcLine(shellName string) (string, error) {
	switch shellName {
	case "bash", "zsh":
		return fmt.Sprintf(`eval "$(aiacc shell-init %s)"`, shellName), nil
	case "fish":
		return "aiacc shell-init fish | source", nil
	default:
		return "", ErrUnknownShell
	}
}

// RcPath is the conventional startup file for a shell, absolute. It does not
// check whether the file exists — callers create it on write.
func RcPath(shellName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shellName {
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	default:
		return "", ErrUnknownShell
	}
}

// aliasNameRe matches a function name aiacc will emit: a letter/underscore
// start, then letters, digits, hyphens, or underscores. Anything else (a space,
// a shell metacharacter) is refused rather than emitted into an eval'd script.
var aliasNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// AccountAlias returns a shell function that switches straight to (provider,
// account) by name — e.g. `claude-work` for ("claude", "work"). It defers to the
// aiacc hook, so it only works alongside it (same shell-init output).
//
// Poka-yoke: when `provider-account` is not a safe function name it returns ""
// with no error, so the caller skips it silently; that account stays reachable
// via `aiacc use`. A bad name is never written into a script the shell evals.
func AccountAlias(shellName, provider, account string) (string, error) {
	name := provider + "-" + account
	if !aliasNameRe.MatchString(name) {
		if shellName != "bash" && shellName != "zsh" && shellName != "fish" {
			return "", ErrUnknownShell
		}
		return "", nil
	}
	switch shellName {
	case "bash", "zsh":
		return fmt.Sprintf("%s() { aiacc use %s %s; }\n", name, provider, account), nil
	case "fish":
		return fmt.Sprintf("function %s; aiacc use %s %s; end\n", name, provider, account), nil
	default:
		return "", ErrUnknownShell
	}
}

// Hook returns the shell function text emitted by `aiacc shell-init <shell>`.
// The function is named aiacc and wraps the real binary: for `use` — and for a
// bare `aiacc` with no arguments, the interactive front door — it evals the
// binary's stdout (passing --shell <shell>) so a chosen switch is applied to the
// current shell; every other subcommand runs the binary directly. `command
// aiacc` prevents the function from recursing into itself.
func Hook(shellName string) (string, error) {
	switch shellName {
	case "bash", "zsh":
		return posixHook(shellName), nil
	case "fish":
		return fishHook, nil
	default:
		return "", ErrUnknownShell
	}
}

// posixHook returns the bash/zsh function body; the two shells share the same
// POSIX syntax and differ only in the --shell argument.
func posixHook(shellName string) string {
	return fmt.Sprintf(`aiacc() {
  if [ "$1" = use ] || [ "$#" -eq 0 ]; then
    local _out
    _out="$(command aiacc "$@" --shell %s)" || return
    eval "$_out"
  else
    command aiacc "$@"
  fi
}
`, shellName)
}

// fishHook is the fish function body; fish uses its own syntax and `set -gx`
// semantics, so it cannot share the POSIX template.
const fishHook = `function aiacc
  if test "$argv[1]" = use; or test (count $argv) -eq 0
    eval (command aiacc $argv --shell fish)
  else
    command aiacc $argv
  end
end
`

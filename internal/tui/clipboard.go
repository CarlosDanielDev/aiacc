package tui

import (
	"os/exec"
	"strings"
)

// clipTools are the system clipboard writers, in the order they're tried: the
// macOS one first (the release's primary target), then Wayland, then the two
// common X11 ones. Each reads the text on stdin.
var clipTools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// clipCopy puts s on the system clipboard using whichever tool is installed.
// It returns false when none is — a missing clipboard is a normal state on a
// bare Linux box, and the caller says so rather than failing the screen.
func clipCopy(s string) bool {
	for _, tool := range clipTools {
		bin, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(bin, tool[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

// copyText is what a message screen puts on the clipboard: the lines painted as
// commands or paths, which is what a "here's how to resume it" screen exists to
// hand over. A screen with none of those copies its whole body instead, so the
// key is never inert.
func copyText(body []Line) string {
	var cmds, all []string
	for _, l := range body {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		all = append(all, l.Text)
		if l.Color == Blue {
			cmds = append(cmds, l.Text)
		}
	}
	if len(cmds) > 0 {
		return strings.Join(cmds, "\n")
	}
	return strings.Join(all, "\n")
}

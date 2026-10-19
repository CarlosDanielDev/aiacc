// Command aiacc switches and monitors multiple AI-CLI accounts via per-account
// config directories and environment variables.
package main

import (
	"fmt"
	"os"

	"github.com/CarlosDanielDev/aiacc/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// A child process's own non-zero status (aiacc ai-memory) is passed on
		// as-is, without an error line of ours on top of its output.
		if code, ok := cli.ExitStatus(err); ok {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "aiacc:", err)
		os.Exit(1)
	}
}

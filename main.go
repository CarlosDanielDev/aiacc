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
		fmt.Fprintln(os.Stderr, "aiacc:", err)
		os.Exit(1)
	}
}

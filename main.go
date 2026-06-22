// Command aiacc switches and monitors multiple AI-CLI accounts via per-account
// config directories and environment variables.
//
// This is a foundation stub. Real subcommands land per the issue graph
// (milestones v0.2.0+). See docs/superpowers/specs for the design.
package main

import "fmt"

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	fmt.Printf("aiacc %s — see https://github.com/CarlosDanielDev/aiacc\n", version)
}

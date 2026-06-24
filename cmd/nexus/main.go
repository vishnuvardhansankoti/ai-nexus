// Package main is the entry point for the Nexus AI agent harness binary.
package main

import "fmt"

// Version and Commit are injected at build time via -ldflags.
var (
	Version = "v0.0.0-dev"
	Commit  = "unknown"
)

func main() {
	fmt.Printf("nexus %s (%s)\n", Version, Commit)
}

// Package main is the entry point for the Nexus AI agent harness binary.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version and Commit are injected at build time via -ldflags.
var (
	Version = "v0.0.0-dev"
	Commit  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "nexus",
	Short: "AI agent harness with 3-layer routing",
	Long:  "Nexus routes prompts to the right model tier automatically, enforces governance, and persists memory across sessions.",
}

func main() {
	rootCmd.AddCommand(
		chatCmd,
		statusCmd,
		configCmd,
		versionCmd,
		serveCmd,
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

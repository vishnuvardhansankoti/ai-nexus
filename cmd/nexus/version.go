package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version and build info",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("nexus %s (%s)\n", Version, Commit)
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the REST API server (implemented in Phase 4)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("REST API server not yet implemented (Phase 4)")
	},
}

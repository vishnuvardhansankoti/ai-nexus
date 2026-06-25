package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Config management commands",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := config.Load(config.DefaultConfigPath()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("OK")
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the resolved config as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(config.DefaultConfigPath())
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	},
}

func init() {
	configCmd.AddCommand(configValidateCmd, configShowCmd)
}

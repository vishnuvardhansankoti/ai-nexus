package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/providers"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show health and availability of all configured layers",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	type layerInfo struct {
		name     string
		provider string
		model    string
		cfg      config.LayerConfig
	}

	layers := []layerInfo{
		{"layer1", cfg.Layers.Layer1.Provider, cfg.Layers.Layer1.Model, cfg.Layers.Layer1},
		{"layer2", cfg.Layers.Layer2.Provider, cfg.Layers.Layer2.Model, cfg.Layers.Layer2},
		{"layer3", cfg.Layers.Layer3.Provider, cfg.Layers.Layer3.Model, cfg.Layers.Layer3},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_ = logger

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LAYER\tPROVIDER\tMODEL\tSTATUS\tLATENCY")

	for _, l := range layers {
		p, err := providers.New(l.cfg)
		if err != nil {
			fmt.Fprintf(w, "%s\t%s\t%s\terror: %v\t-\n", l.name, l.provider, l.model, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		pingErr := p.Ping(ctx)
		latency := time.Since(start)
		cancel()

		status := "up"
		latencyStr := fmt.Sprintf("%dms", latency.Milliseconds())
		if pingErr != nil {
			status = "down"
			latencyStr = "-"
		}
		if l.provider != "ollama" {
			latencyStr = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", l.name, l.provider, l.model, status, latencyStr)
	}

	return w.Flush()
}

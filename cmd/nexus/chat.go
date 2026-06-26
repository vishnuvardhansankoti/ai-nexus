package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/vishnuvardhansankoti/ai-nexus/config"
	"github.com/vishnuvardhansankoti/ai-nexus/memory/episodic"
	"github.com/vishnuvardhansankoti/ai-nexus/memory/working"
	"github.com/vishnuvardhansankoti/ai-nexus/orchestrator"
	"github.com/vishnuvardhansankoti/ai-nexus/sdk"
)

var (
	chatPrompt    string
	chatSessionID string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session (or send a single prompt with --prompt)",
	RunE:  runChat,
}

func init() {
	chatCmd.Flags().StringVarP(&chatPrompt, "prompt", "p", "", "Single-shot prompt; send once and exit")
	chatCmd.Flags().StringVarP(&chatSessionID, "session", "s", "", "Resume an existing session by ID")
}

func runChat(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// --- Episodic store ---
	dbPath := cfg.Memory.Episodic.DB
	if dbPath == "" {
		dbPath = episodic.DefaultDBPath()
	}
	epStore, err := episodic.New(dbPath)
	if err != nil {
		return fmt.Errorf("open episodic store: %w", err)
	}
	defer epStore.Close()

	if cfg.Memory.Episodic.OpenEpisodeTimeout > 0 {
		epStore.StartTimeoutWorker(cmd.Context(), cfg.Memory.Episodic.OpenEpisodeTimeout)
	}

	// --- Orchestrator ---
	orch, err := orchestrator.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init orchestrator: %w", err)
	}
	orch.SetEventStore(epStore)

	// --- Session / episode ---
	sessionID := chatSessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	var episodeID string
	if chatSessionID != "" {
		// Resuming: find the open episode for this session.
		id, found, err := epStore.FindOpenEpisode(sessionID)
		if err != nil {
			return fmt.Errorf("find episode: %w", err)
		}
		if found {
			episodeID = id
		} else {
			episodeID, err = epStore.OpenEpisode(sessionID)
			if err != nil {
				return fmt.Errorf("open episode: %w", err)
			}
		}
	} else {
		episodeID, err = epStore.OpenEpisode(sessionID)
		if err != nil {
			return fmt.Errorf("open episode: %w", err)
		}
	}

	// Attach episode ID to the context so the orchestrator can write events.
	ctx := episodic.WithEpisodeID(cmd.Context(), episodeID)

	// --- Working memory ---
	store, err := working.NewSessionStore(sessionID)
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	defer store.Close()

	var buf working.Buffer

	if chatSessionID != "" {
		msgs, err := store.Load()
		if err != nil {
			return fmt.Errorf("load session: %w", err)
		}
		for _, m := range msgs {
			buf.Append(m)
		}
	}

	closeEpisode := func(outcome string) {
		if err := epStore.CloseEpisode(episodeID, outcome); err != nil {
			logger.Warn("close episode failed", "err", err)
		}
	}

	sendMessage := func(userContent string) error {
		userMsg := sdk.Message{Role: "user", Content: userContent}
		buf.Append(userMsg)
		if err := store.Append(userMsg); err != nil {
			return err
		}

		resp, err := orch.Dispatch(ctx, userContent, sdk.Request{Messages: buf.All()})
		if err != nil {
			return err
		}

		assistantMsg := sdk.Message{Role: "assistant", Content: resp.Content}
		buf.Append(assistantMsg)
		if err := store.Append(assistantMsg); err != nil {
			return err
		}

		fmt.Println(resp.Content)
		return nil
	}

	// Single-shot mode.
	if chatPrompt != "" {
		err := sendMessage(chatPrompt)
		if err != nil {
			closeEpisode(episodic.OutcomeFailure)
			return err
		}
		closeEpisode(episodic.OutcomeSuccess)
		return nil
	}

	// Interactive REPL.
	fmt.Printf("nexus session %s\n", sessionID)
	fmt.Println(`Type "exit" or press Ctrl+C to quit.`)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		closeEpisode(episodic.OutcomePartial)
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") {
			break
		}
		if err := sendMessage(line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		closeEpisode(episodic.OutcomeFailure)
		return err
	}
	closeEpisode(episodic.OutcomeSuccess)
	return nil
}

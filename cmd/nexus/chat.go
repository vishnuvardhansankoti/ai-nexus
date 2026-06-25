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

	orch, err := orchestrator.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init orchestrator: %w", err)
	}

	sessionID := chatSessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	store, err := working.NewSessionStore(sessionID)
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	defer store.Close()

	var buf working.Buffer

	// Load existing messages when resuming.
	if chatSessionID != "" {
		msgs, err := store.Load()
		if err != nil {
			return fmt.Errorf("load session: %w", err)
		}
		for _, m := range msgs {
			buf.Append(m)
		}
	}

	ctx := cmd.Context()

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
		return sendMessage(chatPrompt)
	}

	// Interactive REPL.
	fmt.Printf("nexus session %s\n", sessionID)
	fmt.Println(`Type "exit" or press Ctrl+C to quit.`)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
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
	return scanner.Err()
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crackfetch/brainstorm/internal/config"
	"github.com/crackfetch/brainstorm/internal/mcp"
)

// cmdMCP runs the Model Context Protocol server over stdio. It is a
// long-lived process: every line on stdin is a JSON-RPC request, every
// response is one line on stdout. All log/diagnostic output goes to stderr
// so the JSON-RPC stream stays clean.
func cmdMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	headed := fs.Bool("headed", false, "Show browser window")
	profile := fs.String("profile", "", "Chrome profile directory (defaults to config)")
	idleTimeout := fs.Duration("idle-timeout", 5*time.Minute, "Tear down browser after this much inactivity (0 to disable)")
	debug := fs.Bool("debug", false, "Verbose logging on stderr")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitWorkflowError)
	}

	cfg := config.Load()
	profileDir := *profile
	if profileDir == "" {
		profileDir = cfg.ProfileDir
	}

	logger := log.New(os.Stderr, "[brz mcp] ", log.LstdFlags|log.Lmicroseconds)

	browser := mcp.NewBrowser(mcp.BrowserOptions{
		Headed:     *headed,
		ProfileDir: profileDir,
		Debug:      *debug || cfg.Debug,
	})
	defer browser.Close()

	server := mcp.NewServer(mcp.Config{
		Browser:     browser,
		IdleTimeout: *idleTimeout,
		Logger:      logger,
	})

	// Cancel on SIGINT/SIGTERM so the browser tears down cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			logger.Print("signal received, shutting down")
			cancel()
		case <-ctx.Done():
		}
	}()

	err := server.Run(ctx, os.Stdin, os.Stdout)
	// Tear down the browser BEFORE returning so we never leak Chrome on
	// any exit path. The defer at the top of cmdMCP also runs Close, but
	// if a future change adds os.Exit() below the deferred cleanup would
	// be skipped — close eagerly here too. Close is idempotent.
	browser.Close()
	if err != nil && ctx.Err() == nil {
		logger.Printf("server exited: %v", err)
		os.Exit(exitWorkflowError)
	}
}

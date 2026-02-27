package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/telnesstech/whitenoise-caster/cast"
	"github.com/telnesstech/whitenoise-caster/config"
	"github.com/telnesstech/whitenoise-caster/handlers"
)

//go:embed web/*
var webContent embed.FS

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("loading config", "error", err)
		os.Exit(1)
	}

	logger.Info("config loaded",
		"speakers", len(cfg.Speakers),
		"listen", cfg.ListenAddr,
		"audio_url", cfg.FullAudioURL(),
	)

	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		logger.Error("setting up embedded FS", "error", err)
		os.Exit(1)
	}

	controller := cast.NewController(logger, cfg.FullAudioURL())
	defer controller.Close()

	h := handlers.New(cfg, controller, logger, webFS)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	fmt.Println("goodbye")
}

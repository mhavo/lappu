// Command lappu serves Lappu shares and the viewer.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	lappu "github.com/mhavo/lappu/server"
	"github.com/mhavo/lappu/server/internal/api"
	"github.com/mhavo/lappu/server/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		logger.Warn("LAPPU_ALLOW_ANONYMOUS_UPLOADS=true and no token: anyone can upload")
	}
	st, err := store.New(cfg.DataDir)
	if err != nil {
		return err
	}

	st.SetAttachmentLimits(cfg.MaxAttachmentBytes, cfg.MaxShareAttachmentBytes)

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.New(api.Config{
			Store:          st,
			Token:          cfg.Token,
			AllowAnonymous: cfg.AllowAnonymous,
			MaxBodyBytes:   cfg.MaxBodyBytes,
			TrustedProxies: cfg.TrustedProxies,
			QuotesFile:     cfg.QuotesFile,
			Static:         lappu.Public(),
			Logger:         logger,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		st.CleanupLoop(ctx, time.Hour, logger)
	}()
	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "data", cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	<-cleanupDone // ctx is done, so the loop is exiting
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

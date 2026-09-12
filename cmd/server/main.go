package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"solo-0002-version-compatibility/internal/config"
	"solo-0002-version-compatibility/internal/httpapi"
	"solo-0002-version-compatibility/internal/repository"
	"solo-0002-version-compatibility/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repo, err := repository.Open(cfg.DataDirectory)
	if err != nil {
		return err
	}
	defer repo.Close()
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc, err := service.New(repo, cfg.MaxSteps)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           httpapi.New(svc, logger, cfg.RequestTimeout),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.RequestTimeout,
		WriteTimeout:      cfg.RequestTimeout + 2*time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.Serve(listener) }()
	logger.Info("server ready", "address", listener.Addr().String(), "data_directory", cfg.DataDirectory)
	select {
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		logger.Info("server stopped cleanly")
		return nil
	}
}

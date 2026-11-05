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

	"javis_api/internal/auth"
	"javis_api/internal/auth/smssender"
	"javis_api/internal/config"
	"javis_api/internal/home"
	"javis_api/internal/httpapi"
	"javis_api/pkg/postgres"
)

const authCleanupInterval = time.Hour

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.Default()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, postgres.PoolOptions{
		MaxConns:        5,
		MaxConnLifetime: 30 * time.Minute,
		ConnectTimeout:  cfg.DBHealthTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.OTPSender == config.OTPSenderDebug {
		logger.Warn("OTP debug mode is on: no SMS is sent and codes are returned to callers")
	}
	authService := auth.NewService(pool, auth.Options{
		Pepper:     []byte(cfg.OTPPepper),
		Sender:     smssender.Debug{},
		ExposeCode: cfg.OTPSender == config.OTPSenderDebug,
	})
	go authService.RunCleanup(ctx, authCleanupInterval, logger)

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewHandler(httpapi.Dependencies{
			Logger:        logger,
			Ping:          pool.Ping,
			HealthTimeout: cfg.DBHealthTimeout,
			Auth:          auth.NewHandler(authService),
			Home:          home.NewHandler(home.NewService(pool)),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.ListenAndServe()
	}()
	logger.Info("JAVIS API starting", "address", cfg.HTTPAddr)

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return errors.New("HTTP server graceful shutdown timed out")
		}
		return nil
	}
}

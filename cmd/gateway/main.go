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

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
	"github.com/beasthphp/distributed-api-gateway/internal/config"
	"github.com/beasthphp/distributed-api-gateway/internal/gateway"
	"github.com/beasthphp/distributed-api-gateway/internal/metrics"
	"github.com/beasthphp/distributed-api-gateway/internal/ratelimit"
	"github.com/beasthphp/distributed-api-gateway/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if cfg.APIKeyPepper == "dev-only-pepper-change-me" {
		logger.Warn("using development API key pepper; replace it before deployment")
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startupCancel()
	postgresStore, err := store.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer postgresStore.Close()
	authenticator, err := auth.NewService(postgresStore, cfg.APIKeyPepper)
	if err != nil {
		logger.Error("build authentication service", "error", err)
		os.Exit(1)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("close Redis client", "error", err)
		}
	}()

	limiter := ratelimit.NewRedisLimiter(redisClient, cfg.RateLimitPrefix)
	handler, err := gateway.NewHandler(gateway.Dependencies{
		Config:  cfg,
		Limiter: limiter,
		Auth:    authenticator,
		Readiness: []gateway.HealthCheck{
			{Name: "Redis", Checker: limiter},
			{Name: "PostgreSQL", Checker: postgresStore},
		},
		Metrics: metrics.NewRegistry(),
		Logger:  logger,
	})
	if err != nil {
		logger.Error("build gateway handler", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("gateway started", "address", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("gateway stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdownSignal.Done()
	logger.Info("gateway shutdown started")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway stopped")
}

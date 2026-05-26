package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	gintrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gin-gonic/gin"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sebastien-jorge/rate-limiter/internal/config"
	"github.com/sebastien-jorge/rate-limiter/internal/handler"
	"github.com/sebastien-jorge/rate-limiter/internal/observability"
	"github.com/sebastien-jorge/rate-limiter/internal/service"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	tracer.Start(
		tracer.WithServiceName("rate-limiter"),
		tracer.WithEnv(os.Getenv("DD_ENV")),
		tracer.WithServiceVersion(os.Getenv("DD_VERSION")),
	)
	defer tracer.Stop()

	if err := config.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	policies, err := config.LoadPolicies()
	if err != nil {
		return fmt.Errorf("load policies: %w", err)
	}
	slog.Info("policies loaded", "count", len(policies))

	var metricsAddr string
	if host := os.Getenv("DD_AGENT_HOST"); host != "" {
		metricsAddr = host + ":8125"
	}
	m, err := observability.NewMetrics(metricsAddr)
	if err != nil {
		return fmt.Errorf("init metrics: %w", err)
	}
	defer func() { _ = m.Close() }()

	store, err := storage.NewHybridStore(config.LoadRedisConfig(), "rl:")
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			slog.Error("failed to close store", "error", err)
		}
	}()

	rl := service.NewRateLimiter(store)
	pm := service.NewPolicyMatcher(policies)
	h := handler.New(rl, pm, store, m)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gintrace.Middleware("rate-limiter"))
	router.Use(handler.RequestID())
	router.Use(handler.Logger())
	router.Use(handler.MetricsMiddleware(m))
	h.RegisterRoutes(router)

	srvCfg := config.LoadServerConfig()
	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(srvCfg.Port),
		Handler:      router,
		ReadTimeout:  srvCfg.Timeout,
		WriteTimeout: srvCfg.Timeout,
	}

	go func() {
		slog.Info("server listening", "port", srvCfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	slog.Info("server stopped")
	return nil
}

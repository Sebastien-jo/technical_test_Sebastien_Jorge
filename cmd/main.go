package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/sebastien-jorge/rate-limiter/internal/config"
	"github.com/sebastien-jorge/rate-limiter/internal/handler"
	"github.com/sebastien-jorge/rate-limiter/internal/service"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
)

func main() {
	if err := loadConfig(); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	policies, err := config.LoadPolicies()
	if err != nil {
		log.Fatalf("failed to load policies: %v", err)
	}
	log.Printf("loaded %d client policies", len(policies))

	store, err := storage.NewHybridStore(
		viper.GetString("redis.host"),
		viper.GetInt("redis.port"),
		viper.GetInt("redis.db"),
		"rl:",
	)
	if err != nil {
		log.Fatalf("failed to create storage: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("error closing store: %v", err)
		}
	}()

	rl := service.NewRateLimiter()
	pm := service.NewPolicyMatcher(policies)
	h := handler.New(rl, pm, store)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(handler.RequestID())
	router.Use(handler.Logger())
	h.RegisterRoutes(router)

	port := viper.GetString("server.port")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  viper.GetDuration("server.timeout"),
		WriteTimeout: viper.GetDuration("server.timeout"),
	}

	go func() {
		log.Printf("server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	if err := srv.Shutdown(ctx); err != nil {
		cancel()
		log.Printf("forced shutdown: %v", err)
		return
	}
	cancel()
	log.Println("server stopped")
}

func loadConfig() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/app")

	viper.AutomaticEnv()

	viper.SetDefault("server.port", "8080")
	viper.SetDefault("server.timeout", "30s")
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("no config file found, using defaults and environment variables")
			return nil
		}
		return err
	}

	log.Printf("config loaded from: %s", viper.ConfigFileUsed())
	return nil
}

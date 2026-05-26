package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/spf13/viper"
)

type RouteConfig struct {
	Route      string `mapstructure:"route"`
	RouteType  string `mapstructure:"route_type"`
	Method     string `mapstructure:"method"`
	Limit      int64  `mapstructure:"limit"`
	Window     string `mapstructure:"window"`
	Identifier string `mapstructure:"identifier"`
}

type PolicyConfig struct {
	ClientID string        `mapstructure:"client_id"`
	Routes   []RouteConfig `mapstructure:"routes"`
}

// Load initialises Viper: registers defaults, sets the env-var key replacer
// (so REDIS_HOST resolves to redis.host), and reads the optional config file.
// Missing file is not an error — defaults + env vars are sufficient.
func Load() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/app")

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			slog.Info("no config file found, using defaults and environment variables")
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}
	slog.Info("config loaded", "file", viper.ConfigFileUsed())
	return nil
}

func setDefaults() {
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.timeout", "30s")
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.tls", false)
}

// LoadServerConfig reads the HTTP server settings.
func LoadServerConfig() models.ServerConfig {
	return models.ServerConfig{
		Port:    viper.GetInt("server.port"),
		Timeout: viper.GetDuration("server.timeout"),
	}
}

// LoadRedisConfig reads the Redis connection settings.
func LoadRedisConfig() models.RedisConfig {
	return models.RedisConfig{
		Host:     viper.GetString("redis.host"),
		Port:     viper.GetInt("redis.port"),
		DB:       viper.GetInt("redis.db"),
		Password: viper.GetString("redis.password"),
		TLS:      viper.GetBool("redis.tls"),
	}
}

func (rc RouteConfig) ToRoutePolicy() (models.RoutePolicy, error) {
	if rc.Identifier == "" {
		rc.Identifier = string(models.IdentifierNone)
	}
	d, err := time.ParseDuration(rc.Window)
	if err != nil {
		return models.RoutePolicy{}, fmt.Errorf("invalid window %q: %w", rc.Window, err)
	}
	rp := models.RoutePolicy{
		Route:      rc.Route,
		RouteType:  models.RouteType(rc.RouteType),
		Method:     rc.Method,
		Limit:      rc.Limit,
		Window:     d,
		Identifier: models.IdentifierType(rc.Identifier),
	}
	if err := rp.Validate(); err != nil {
		return models.RoutePolicy{}, err
	}
	return rp, nil
}

func (pc PolicyConfig) ToClientPolicy() (*models.ClientPolicy, error) {
	cp := &models.ClientPolicy{
		ClientID: models.ClientID(pc.ClientID),
	}
	for _, rc := range pc.Routes {
		rp, err := rc.ToRoutePolicy()
		if err != nil {
			return nil, fmt.Errorf("client %q route %q: %w", pc.ClientID, rc.Route, err)
		}
		cp.Routes = append(cp.Routes, rp)
	}
	return cp, nil
}

func LoadPolicies() ([]*models.ClientPolicy, error) {
	var cfgs []PolicyConfig
	if err := viper.UnmarshalKey("policies", &cfgs); err != nil {
		return nil, fmt.Errorf("unmarshal policies: %w", err)
	}

	policies := make([]*models.ClientPolicy, 0, len(cfgs))
	for _, cfg := range cfgs {
		cp, err := cfg.ToClientPolicy()
		if err != nil {
			return nil, err
		}
		policies = append(policies, cp)
	}
	return policies, nil
}

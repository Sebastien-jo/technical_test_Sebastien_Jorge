package config

import (
	"fmt"
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

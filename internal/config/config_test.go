package config

import (
	"testing"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validRouteConfig() RouteConfig {
	return RouteConfig{
		Route:      "/api",
		RouteType:  "prefix",
		Method:     "GET",
		Limit:      100,
		Window:     "1m",
		Identifier: "none",
	}
}

// ── ToRoutePolicy ─────────────────────────────────────────────────────────────

func TestToRoutePolicy_Valid(t *testing.T) {
	rc := validRouteConfig()
	rp, err := rc.ToRoutePolicy()
	require.NoError(t, err)
	assert.Equal(t, "/api", rp.Route)
	assert.Equal(t, models.RoutePrefix, rp.RouteType)
	assert.Equal(t, "GET", rp.Method)
	assert.Equal(t, int64(100), rp.Limit)
	assert.Equal(t, models.IdentifierNone, rp.Identifier)
}

func TestToRoutePolicy_DefaultsIdentifierToNone(t *testing.T) {
	rc := validRouteConfig()
	rc.Identifier = ""
	rp, err := rc.ToRoutePolicy()
	require.NoError(t, err)
	assert.Equal(t, models.IdentifierNone, rp.Identifier)
}

func TestToRoutePolicy_InvalidWindow(t *testing.T) {
	rc := validRouteConfig()
	rc.Window = "not-a-duration"
	_, err := rc.ToRoutePolicy()
	assert.Error(t, err)
}

func TestToRoutePolicy_ValidationFails(t *testing.T) {
	rc := validRouteConfig()
	rc.Limit = 0
	_, err := rc.ToRoutePolicy()
	assert.Error(t, err)
}

func TestToRoutePolicy_ExactRouteType(t *testing.T) {
	rc := validRouteConfig()
	rc.RouteType = "exact"
	rp, err := rc.ToRoutePolicy()
	require.NoError(t, err)
	assert.Equal(t, models.RouteExact, rp.RouteType)
}

func TestToRoutePolicy_AllIdentifiers(t *testing.T) {
	for _, id := range []string{"none", "session_id", "ip", "ip_user_agent"} {
		rc := validRouteConfig()
		rc.Identifier = id
		_, err := rc.ToRoutePolicy()
		assert.NoError(t, err, "identifier %q should be valid", id)
	}
}

// ── ToClientPolicy ────────────────────────────────────────────────────────────

func TestToClientPolicy_Valid(t *testing.T) {
	pc := PolicyConfig{
		ClientID: "app",
		Routes:   []RouteConfig{validRouteConfig()},
	}
	cp, err := pc.ToClientPolicy()
	require.NoError(t, err)
	assert.Equal(t, models.ClientID("app"), cp.ClientID)
	require.Len(t, cp.Routes, 1)
	assert.Equal(t, "/api", cp.Routes[0].Route)
}

func TestToClientPolicy_MultipleRoutes(t *testing.T) {
	rc2 := validRouteConfig()
	rc2.Route = "/api/videos"
	rc2.RouteType = "exact"

	pc := PolicyConfig{
		ClientID: "app",
		Routes:   []RouteConfig{validRouteConfig(), rc2},
	}
	cp, err := pc.ToClientPolicy()
	require.NoError(t, err)
	assert.Len(t, cp.Routes, 2)
}

func TestToClientPolicy_InvalidRoutePropagatesError(t *testing.T) {
	bad := validRouteConfig()
	bad.Window = "bad"
	pc := PolicyConfig{ClientID: "app", Routes: []RouteConfig{bad}}
	_, err := pc.ToClientPolicy()
	assert.Error(t, err)
}

func TestToClientPolicy_EmptyRoutes(t *testing.T) {
	pc := PolicyConfig{ClientID: "app", Routes: nil}
	cp, err := pc.ToClientPolicy()
	require.NoError(t, err)
	assert.Empty(t, cp.Routes)
}

// ── LoadPolicies ──────────────────────────────────────────────────────────────

func setupViper(policies []map[string]interface{}) {
	viper.Reset()
	viper.Set("policies", policies)
}

func TestLoadPolicies_Valid(t *testing.T) {
	setupViper([]map[string]interface{}{
		{
			"client_id": "partner",
			"routes": []map[string]interface{}{
				{
					"route":      "/api/videos",
					"route_type": "prefix",
					"method":     "GET",
					"limit":      2000,
					"window":     "1m",
					"identifier": "none",
				},
			},
		},
	})
	t.Cleanup(viper.Reset)

	policies, err := LoadPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, models.ClientID("partner"), policies[0].ClientID)
}

func TestLoadPolicies_Empty(t *testing.T) {
	setupViper(nil)
	t.Cleanup(viper.Reset)

	policies, err := LoadPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestLoadPolicies_MultipleClients(t *testing.T) {
	setupViper([]map[string]interface{}{
		{
			"client_id": "client-a",
			"routes": []map[string]interface{}{
				{"route": "/a", "route_type": "exact", "method": "GET", "limit": 10, "window": "1m", "identifier": "none"},
			},
		},
		{
			"client_id": "client-b",
			"routes": []map[string]interface{}{
				{"route": "/b", "route_type": "exact", "method": "POST", "limit": 5, "window": "30s", "identifier": "ip"},
			},
		},
	})
	t.Cleanup(viper.Reset)

	policies, err := LoadPolicies()
	require.NoError(t, err)
	assert.Len(t, policies, 2)
}

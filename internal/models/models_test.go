package models

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ValidationError ───────────────────────────────────────────────────────────

func TestValidationError_Error(t *testing.T) {
	ve := &ValidationError{Field: "route", Message: "cannot be empty"}
	assert.Contains(t, ve.Error(), "route")
	assert.Contains(t, ve.Error(), "cannot be empty")
}

// ── RedisConfig.Addr ──────────────────────────────────────────────────────────

func TestRedisConfig_Addr(t *testing.T) {
	rc := RedisConfig{Host: "localhost", Port: 6379}
	assert.Equal(t, "localhost:6379", rc.Addr())
}

// ── QuotaKey.String ───────────────────────────────────────────────────────────

func TestQuotaKey_String_Format(t *testing.T) {
	qk := QuotaKey{ClientID: "app", Route: "/api", Method: "GET", Identifier: "global"}
	s := qk.String()
	assert.True(t, strings.HasPrefix(s, "rl:"), "should start with rl:")
	assert.Contains(t, s, "app")
	assert.Contains(t, s, "/api")
	assert.Contains(t, s, "GET")
	assert.Contains(t, s, "global")
}

func TestQuotaKey_String_Unique(t *testing.T) {
	a := QuotaKey{ClientID: "a", Route: "/api", Method: "GET", Identifier: "x"}.String()
	b := QuotaKey{ClientID: "b", Route: "/api", Method: "GET", Identifier: "x"}.String()
	assert.NotEqual(t, a, b)
}

// ── RoutePolicy.Validate ──────────────────────────────────────────────────────

func validRoutePolicy() RoutePolicy {
	return RoutePolicy{
		Route:      "/api",
		RouteType:  RoutePrefix,
		Method:     "GET",
		Limit:      100,
		Window:     time.Minute,
		Identifier: IdentifierNone,
	}
}

func TestRoutePolicy_Validate_Valid(t *testing.T) {
	rp := validRoutePolicy()
	assert.NoError(t, rp.Validate())
}

func TestRoutePolicy_Validate_EmptyRoute(t *testing.T) {
	rp := validRoutePolicy()
	rp.Route = ""
	assert.Error(t, rp.Validate())
}

func TestRoutePolicy_Validate_InvalidRouteType(t *testing.T) {
	rp := validRoutePolicy()
	rp.RouteType = "fuzzy"
	assert.Error(t, rp.Validate())
}

func TestRoutePolicy_Validate_EmptyMethod(t *testing.T) {
	rp := validRoutePolicy()
	rp.Method = ""
	assert.Error(t, rp.Validate())
}

func TestRoutePolicy_Validate_ZeroLimit(t *testing.T) {
	rp := validRoutePolicy()
	rp.Limit = 0
	assert.ErrorIs(t, rp.Validate(), ErrInvalidLimit)
}

func TestRoutePolicy_Validate_NegativeLimit(t *testing.T) {
	rp := validRoutePolicy()
	rp.Limit = -1
	assert.ErrorIs(t, rp.Validate(), ErrInvalidLimit)
}

func TestRoutePolicy_Validate_ZeroWindow(t *testing.T) {
	rp := validRoutePolicy()
	rp.Window = 0
	assert.ErrorIs(t, rp.Validate(), ErrInvalidWindow)
}

func TestRoutePolicy_Validate_UnknownIdentifier(t *testing.T) {
	rp := validRoutePolicy()
	rp.Identifier = "banana"
	assert.Error(t, rp.Validate())
}

func TestRoutePolicy_Validate_AllIdentifiers(t *testing.T) {
	for _, id := range []IdentifierType{IdentifierNone, IdentifierSessionID, IdentifierIPUserAgent} {
		rp := validRoutePolicy()
		rp.Identifier = id
		assert.NoError(t, rp.Validate(), "identifier %q should be valid", id)
	}
}

func TestRoutePolicy_Validate_WildcardMethod(t *testing.T) {
	rp := validRoutePolicy()
	rp.Method = "*"
	assert.NoError(t, rp.Validate())
}

// ── ClientPolicy.Validate ─────────────────────────────────────────────────────

func validClientPolicy() *ClientPolicy {
	return &ClientPolicy{
		ClientID: "test-client",
		Routes:   []RoutePolicy{validRoutePolicy()},
	}
}

func TestClientPolicy_Validate_Valid(t *testing.T) {
	cp := validClientPolicy()
	assert.NoError(t, cp.Validate())
}

func TestClientPolicy_Validate_EmptyClientID(t *testing.T) {
	cp := validClientPolicy()
	cp.ClientID = ""
	assert.Error(t, cp.Validate())
}

func TestClientPolicy_Validate_InvalidRoute(t *testing.T) {
	cp := validClientPolicy()
	cp.Routes[0].Limit = -1
	assert.Error(t, cp.Validate())
}

func TestClientPolicy_Validate_DuplicateRouteMethod(t *testing.T) {
	rp := validRoutePolicy()
	cp := &ClientPolicy{
		ClientID: "client",
		Routes:   []RoutePolicy{rp, rp},
	}
	assert.ErrorIs(t, cp.Validate(), ErrDuplicateRoute)
}

func TestClientPolicy_Validate_SameRouteDifferentMethods(t *testing.T) {
	rp1 := validRoutePolicy()
	rp1.Method = "GET"
	rp2 := validRoutePolicy()
	rp2.Method = "POST"
	cp := &ClientPolicy{ClientID: "client", Routes: []RoutePolicy{rp1, rp2}}
	assert.NoError(t, cp.Validate())
}

// ── ClientPolicy.FindMatchingRoute ────────────────────────────────────────────

func TestFindMatchingRoute_ExactMatch(t *testing.T) {
	cp := &ClientPolicy{
		ClientID: "app",
		Routes: []RoutePolicy{
			{Route: "/api/videos", RouteType: RouteExact, Method: "GET", Limit: 100, Window: time.Minute, Identifier: IdentifierNone},
		},
	}
	rp := cp.FindMatchingRoute("/api/videos", "GET")
	require.NotNil(t, rp)
	assert.Equal(t, "/api/videos", rp.Route)
}

func TestFindMatchingRoute_ExactDoesNotMatchSubPath(t *testing.T) {
	cp := &ClientPolicy{
		ClientID: "app",
		Routes: []RoutePolicy{
			{Route: "/api/videos", RouteType: RouteExact, Method: "GET", Limit: 100, Window: time.Minute, Identifier: IdentifierNone},
		},
	}
	assert.Nil(t, cp.FindMatchingRoute("/api/videos/123", "GET"))
}

func TestFindMatchingRoute_PrefixMatchesSubPaths(t *testing.T) {
	cp := &ClientPolicy{
		ClientID: "app",
		Routes: []RoutePolicy{
			{Route: "/api", RouteType: RoutePrefix, Method: "*", Limit: 100, Window: time.Minute, Identifier: IdentifierNone},
		},
	}
	assert.NotNil(t, cp.FindMatchingRoute("/api", "*"))
	assert.NotNil(t, cp.FindMatchingRoute("/api/videos", "GET"))
	assert.NotNil(t, cp.FindMatchingRoute("/api/videos/123", "POST"))
}

func TestFindMatchingRoute_LongestPrefixWins(t *testing.T) {
	cp := &ClientPolicy{
		ClientID: "app",
		Routes: []RoutePolicy{
			{Route: "/api", RouteType: RoutePrefix, Method: "*", Limit: 500, Window: time.Minute, Identifier: IdentifierNone},
			{Route: "/api/videos", RouteType: RoutePrefix, Method: "*", Limit: 100, Window: time.Minute, Identifier: IdentifierNone},
		},
	}
	rp := cp.FindMatchingRoute("/api/videos/123", "GET")
	require.NotNil(t, rp)
	assert.Equal(t, int64(100), rp.Limit)
}

func TestFindMatchingRoute_MethodFilter(t *testing.T) {
	cp := &ClientPolicy{
		ClientID: "app",
		Routes: []RoutePolicy{
			{Route: "/api", RouteType: RouteExact, Method: "POST", Limit: 10, Window: time.Minute, Identifier: IdentifierNone},
		},
	}
	assert.Nil(t, cp.FindMatchingRoute("/api", "GET"))
	assert.NotNil(t, cp.FindMatchingRoute("/api", "POST"))
}

func TestFindMatchingRoute_NoMatch(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app", Routes: []RoutePolicy{}}
	assert.Nil(t, cp.FindMatchingRoute("/api", "GET"))
}

// ── pathPrefixMatches ─────────────────────────────────────────────────────────

func TestPathPrefixMatches_RootMatchesAll(t *testing.T) {
	assert.True(t, pathPrefixMatches("/anything", "/"))
	assert.True(t, pathPrefixMatches("/", "/"))
}

func TestPathPrefixMatches_ExactPath(t *testing.T) {
	assert.True(t, pathPrefixMatches("/api", "/api"))
}

func TestPathPrefixMatches_SubPath(t *testing.T) {
	assert.True(t, pathPrefixMatches("/api/videos", "/api"))
}

func TestPathPrefixMatches_NoBoundary(t *testing.T) {
	assert.False(t, pathPrefixMatches("/apiv2", "/api"))
}

func TestPathPrefixMatches_NoPrefix(t *testing.T) {
	assert.False(t, pathPrefixMatches("/other", "/api"))
}

// ── ClientPolicy.AddRoute ─────────────────────────────────────────────────────

func TestAddRoute_AddsValidRoute(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app"}
	rp := validRoutePolicy()
	err := cp.AddRoute(rp)
	require.NoError(t, err)
	assert.Len(t, cp.Routes, 1)
	assert.False(t, cp.UpdatedAt.IsZero())
}

func TestAddRoute_RejectsDuplicate(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app"}
	rp := validRoutePolicy()
	require.NoError(t, cp.AddRoute(rp))
	assert.ErrorIs(t, cp.AddRoute(rp), ErrDuplicateRoute)
}

func TestAddRoute_RejectsInvalidRoute(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app"}
	rp := validRoutePolicy()
	rp.Limit = 0
	assert.Error(t, cp.AddRoute(rp))
}

// ── ClientPolicy.RemoveRoute ──────────────────────────────────────────────────

func TestRemoveRoute_RemovesExisting(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app"}
	rp := validRoutePolicy()
	require.NoError(t, cp.AddRoute(rp))

	err := cp.RemoveRoute(rp.Route, rp.Method)
	require.NoError(t, err)
	assert.Empty(t, cp.Routes)
	assert.False(t, cp.UpdatedAt.IsZero())
}

func TestRemoveRoute_ErrorOnMissing(t *testing.T) {
	cp := &ClientPolicy{ClientID: "app"}
	assert.ErrorIs(t, cp.RemoveRoute("/not-there", "GET"), ErrRouteNotFound)
}

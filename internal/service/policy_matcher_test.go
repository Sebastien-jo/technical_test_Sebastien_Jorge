package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

func makeRoutePolicy(route string, routeType models.RouteType, method string, limit int64) models.RoutePolicy {
	return models.RoutePolicy{
		Route:      route,
		RouteType:  routeType,
		Method:     method,
		Limit:      limit,
		Window:     time.Minute,
		Identifier: models.IdentifierNone,
	}
}

func makeClientPolicy(clientID string, routes ...models.RoutePolicy) *models.ClientPolicy {
	return &models.ClientPolicy{
		ID:       models.PolicyID("policy:" + clientID),
		ClientID: models.ClientID(clientID),
		Routes:   routes,
	}
}

func TestFindPolicy_ExactRoute_Matches(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/videos", models.RouteExact, "GET", 100),
		),
	})

	rp, err := pm.FindPolicy("app", "/api/videos", "GET")
	require.NoError(t, err)
	assert.Equal(t, "/api/videos", rp.Route)
	assert.Equal(t, int64(100), rp.Limit)
}

func TestFindPolicy_ExactRoute_NoMatchOnSubPath(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/videos", models.RouteExact, "GET", 100),
		),
	})

	_, err := pm.FindPolicy("app", "/api/videos/123", "GET")
	assert.ErrorIs(t, err, models.ErrRouteNotFound)
}

func TestFindPolicy_PrefixRoute_MatchesSubPaths(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "GET", 100),
		),
	})

	for _, route := range []string{"/api", "/api/videos", "/api/videos/123"} {
		_, err := pm.FindPolicy("app", route, "GET")
		assert.NoError(t, err, "prefix /api should match %q", route)
	}
}

func TestFindPolicy_PrefixRoute_NoMatchWithoutSeparator(t *testing.T) {
	// "/api" must NOT match the request path "/apiv2" — there is no "/" boundary.
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "GET", 100),
		),
	})

	_, err := pm.FindPolicy("app", "/apiv2", "GET")
	assert.ErrorIs(t, err, models.ErrRouteNotFound)
}

func TestFindPolicy_RootPrefix_MatchesEverything(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/", models.RoutePrefix, "*", 1000),
		),
	})

	for _, route := range []string{"/", "/api/videos", "/anything/at/all"} {
		_, err := pm.FindPolicy("app", route, "GET")
		assert.NoError(t, err, "root prefix should match %q", route)
	}
}

func TestFindPolicy_WildcardMethod_MatchesAny(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
	})

	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		_, err := pm.FindPolicy("app", "/api/resource", method)
		assert.NoError(t, err, "wildcard should match method %q", method)
	}
}

func TestFindPolicy_SpecificMethod_NoMatchOnOtherMethods(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/users", models.RouteExact, "POST", 10),
		),
	})

	_, err := pm.FindPolicy("app", "/api/users", "POST")
	require.NoError(t, err)

	_, err = pm.FindPolicy("app", "/api/users", "GET")
	assert.ErrorIs(t, err, models.ErrRouteNotFound)
}

func TestFindPolicy_MethodIsCaseInsensitive(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RouteExact, "GET", 100),
		),
	})

	_, err := pm.FindPolicy("app", "/api", "get")
	assert.NoError(t, err)
}

func TestFindPolicy_ExactBeforePrefix(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 500),
			makeRoutePolicy("/api/videos", models.RouteExact, "GET", 100),
		),
	})

	rp, err := pm.FindPolicy("app", "/api/videos", "GET")
	require.NoError(t, err)
	assert.Equal(t, int64(100), rp.Limit, "exact (100) should beat prefix (500)")
}

func TestFindPolicy_LongestPrefixWins(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 500),
			makeRoutePolicy("/api/videos", models.RoutePrefix, "*", 100),
		),
	})

	rp, err := pm.FindPolicy("app", "/api/videos/123", "GET")
	require.NoError(t, err)
	assert.Equal(t, int64(100), rp.Limit, "/api/videos (100) should beat /api (500)")
}

func TestFindPolicy_UnknownClient(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
	})

	_, err := pm.FindPolicy("unknown", "/api", "GET")
	assert.ErrorIs(t, err, models.ErrClientNotFound)
}

func TestFindPolicy_ClientWithNoRoutes(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app"), // no routes
	})

	_, err := pm.FindPolicy("app", "/api/videos", "GET")
	assert.ErrorIs(t, err, models.ErrPolicyNotFound)
}

func TestFindPolicy_MultiplePoliciesForSameClient(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/videos", models.RouteExact, "GET", 100),
			makeRoutePolicy("/api/videos", models.RouteExact, "POST", 10),
			makeRoutePolicy("/api/users", models.RouteExact, "*", 50),
		),
	})

	rp, _ := pm.FindPolicy("app", "/api/videos", "GET")
	assert.Equal(t, int64(100), rp.Limit)

	rp, _ = pm.FindPolicy("app", "/api/videos", "POST")
	assert.Equal(t, int64(10), rp.Limit)

	rp, _ = pm.FindPolicy("app", "/api/users", "PUT")
	assert.Equal(t, int64(50), rp.Limit)
}

func TestFindPolicy_SimilarPrefixesDontInterfere(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
			makeRoutePolicy("/apiv2", models.RoutePrefix, "*", 200),
		),
	})

	rp, _ := pm.FindPolicy("app", "/api/test", "GET")
	assert.Equal(t, int64(100), rp.Limit, "/api/test → /api")

	rp, _ = pm.FindPolicy("app", "/apiv2/test", "GET")
	assert.Equal(t, int64(200), rp.Limit, "/apiv2/test → /apiv2")
}

func TestFindPolicy_MultipleClients_NoLeakBetweenThem(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("mobile",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
		makeClientPolicy("partner",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 1000),
		),
	})

	rp, _ := pm.FindPolicy("mobile", "/api/data", "GET")
	assert.Equal(t, int64(100), rp.Limit)

	rp, _ = pm.FindPolicy("partner", "/api/data", "GET")
	assert.Equal(t, int64(1000), rp.Limit)
}

func TestFindPolicy_RouteIsCaseSensitive(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/Videos", models.RouteExact, "GET", 100),
		),
	})

	_, err := pm.FindPolicy("app", "/api/Videos", "GET")
	assert.NoError(t, err)

	_, err = pm.FindPolicy("app", "/api/videos", "GET")
	assert.ErrorIs(t, err, models.ErrRouteNotFound)
}

func TestUpdatePolicies_ReplacesExistingRules(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
	})

	rp, _ := pm.FindPolicy("app", "/api/videos", "GET")
	assert.Equal(t, int64(100), rp.Limit)

	pm.UpdatePolicies([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 500),
		),
	})

	rp, _ = pm.FindPolicy("app", "/api/videos", "GET")
	assert.Equal(t, int64(500), rp.Limit)
}

func TestUpdatePolicies_RemovesOldClients(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("old-client",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
	})

	pm.UpdatePolicies([]*models.ClientPolicy{
		makeClientPolicy("new-client",
			makeRoutePolicy("/api", models.RoutePrefix, "*", 100),
		),
	})

	_, err := pm.FindPolicy("old-client", "/api", "GET")
	assert.ErrorIs(t, err, models.ErrClientNotFound)
}

func TestGetPoliciesForClient_ReturnsAllRoutes(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("app",
			makeRoutePolicy("/api/videos", models.RouteExact, "GET", 100),
			makeRoutePolicy("/api/users", models.RouteExact, "POST", 10),
		),
	})

	routes := pm.GetPoliciesForClient("app")
	assert.Len(t, routes, 2)
	assert.Nil(t, pm.GetPoliciesForClient("unknown"))
}

func TestGetAllClients_ReturnsEveryConfiguredClient(t *testing.T) {
	pm := NewPolicyMatcher([]*models.ClientPolicy{
		makeClientPolicy("mobile"),
		makeClientPolicy("partner"),
		makeClientPolicy("internal"),
	})

	clients := pm.GetAllClients()
	assert.ElementsMatch(t, []string{"mobile", "partner", "internal"}, clients)
}

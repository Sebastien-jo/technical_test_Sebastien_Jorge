package service

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

type PolicyMatcher struct {
	mu       sync.RWMutex
	policies map[string]*models.ClientPolicy
}

func NewPolicyMatcher(policies []*models.ClientPolicy) *PolicyMatcher {
	pm := &PolicyMatcher{
		policies: make(map[string]*models.ClientPolicy, len(policies)),
	}
	for _, cp := range policies {
		pm.policies[string(cp.ClientID)] = cp
	}
	return pm
}

func (pm *PolicyMatcher) FindPolicy(clientID, route, method string) (*models.RoutePolicy, error) {
	pm.mu.RLock()
	cp, exists := pm.policies[clientID]
	pm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("%w: %q", models.ErrClientNotFound, clientID)
	}
	if len(cp.Routes) == 0 {
		return nil, fmt.Errorf("%w: client %q has no routes configured", models.ErrPolicyNotFound, clientID)
	}

	rp := findBestRoute(cp.Routes, route, method)
	if rp == nil {
		return nil, fmt.Errorf("%w: route=%q method=%q client=%q",
			models.ErrRouteNotFound, route, method, clientID)
	}
	return rp, nil
}

func (pm *PolicyMatcher) UpdatePolicies(policies []*models.ClientPolicy) {
	updated := make(map[string]*models.ClientPolicy, len(policies))
	for _, cp := range policies {
		updated[string(cp.ClientID)] = cp
	}
	pm.mu.Lock()
	pm.policies = updated
	pm.mu.Unlock()
}

func (pm *PolicyMatcher) GetPoliciesForClient(clientID string) []models.RoutePolicy {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cp, ok := pm.policies[clientID]
	if !ok {
		return nil
	}
	return cp.Routes
}

func (pm *PolicyMatcher) GetAllClients() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	clients := make([]string, 0, len(pm.policies))
	for id := range pm.policies {
		clients = append(clients, id)
	}
	return clients
}

func findBestRoute(routes []models.RoutePolicy, route, method string) *models.RoutePolicy {
	// Pass 1 — exact match (highest priority).
	for i := range routes {
		rp := &routes[i]
		if rp.RouteType == models.RouteExact &&
			rp.Route == route &&
			methodMatches(rp.Method, method) {
			return rp
		}
	}

	// Pass 2 — prefix match, longest wins.
	var best *models.RoutePolicy
	for i := range routes {
		rp := &routes[i]
		if rp.RouteType != models.RoutePrefix {
			continue
		}
		if !prefixMatches(route, rp.Route) || !methodMatches(rp.Method, method) {
			continue
		}
		if best == nil || len(rp.Route) > len(best.Route) {
			best = rp
		}
	}
	return best
}

// methodMatches returns true when policyMethod is "*" or equals requestMethod
// (case-insensitive, following HTTP spec).
func methodMatches(policyMethod, requestMethod string) bool {
	return policyMethod == "*" || strings.EqualFold(policyMethod, requestMethod)
}

// "/"         → matches everything
// "/api"      → matches "/api", "/api/", "/api/videos"
// "/api"      → does NOT match "/apiv2" (no "/" separator after prefix)
func prefixMatches(requestRoute, prefix string) bool {
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(requestRoute, prefix) {
		return false
	}
	// Guard against "/api" matching "/apiv2": next char must be "/" or end of string.
	return len(requestRoute) == len(prefix) || requestRoute[len(prefix)] == '/'
}

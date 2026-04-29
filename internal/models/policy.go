package models

import (
	"fmt"
	"strings"
	"time"
)

type (
	PolicyID string
	ClientID string
)

type RouteType string

const (
	RouteExact  RouteType = "exact"
	RoutePrefix RouteType = "prefix"
)

type IdentifierType string

const (
	IdentifierNone        IdentifierType = "none"
	IdentifierSessionID   IdentifierType = "session_id"
	IdentifierIPUserAgent IdentifierType = "ip_user_agent"
)

type RoutePolicy struct {
	Route      string
	RouteType  RouteType
	Method     string
	Limit      int64
	Window     time.Duration
	Identifier IdentifierType
}

func (rp *RoutePolicy) Validate() error {
	if rp.Route == "" {
		return &ValidationError{Field: "route", Message: "cannot be empty"}
	}
	if rp.RouteType != RouteExact && rp.RouteType != RoutePrefix {
		return &ValidationError{
			Field:   "route_type",
			Message: fmt.Sprintf("must be %q or %q, got %q", RouteExact, RoutePrefix, rp.RouteType),
		}
	}
	if rp.Method == "" {
		return &ValidationError{Field: "method", Message: "cannot be empty"}
	}
	if rp.Limit <= 0 {
		return ErrInvalidLimit
	}
	if rp.Window <= 0 {
		return ErrInvalidWindow
	}
	switch rp.Identifier {
	case IdentifierNone, IdentifierSessionID, IdentifierIPUserAgent:
	default:
		return &ValidationError{
			Field:   "identifier",
			Message: fmt.Sprintf("unknown identifier type: %q", rp.Identifier),
		}
	}
	return nil
}

type ClientPolicy struct {
	ID        PolicyID
	ClientID  ClientID
	Routes    []RoutePolicy
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (cp *ClientPolicy) Validate() error {
	if cp.ClientID == "" {
		return &ValidationError{Field: "client_id", Message: "cannot be empty"}
	}
	seen := make(map[string]struct{}, len(cp.Routes))
	for i := range cp.Routes {
		if err := cp.Routes[i].Validate(); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
		key := cp.Routes[i].Route + "|" + cp.Routes[i].Method
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: route=%q method=%q", ErrDuplicateRoute, cp.Routes[i].Route, cp.Routes[i].Method)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (cp *ClientPolicy) FindMatchingRoute(route, method string) *RoutePolicy {
	var bestPrefix *RoutePolicy

	for i := range cp.Routes {
		rp := &cp.Routes[i]

		if rp.Method != "*" && !strings.EqualFold(rp.Method, method) {
			continue
		}

		switch rp.RouteType {
		case RouteExact:
			if rp.Route == route {
				return rp
			}
		case RoutePrefix:
			if pathPrefixMatches(route, rp.Route) {
				if bestPrefix == nil || len(rp.Route) > len(bestPrefix.Route) {
					bestPrefix = rp
				}
			}
		}
	}

	return bestPrefix
}

// pathPrefixMatches reports whether route starts with prefix at a path boundary.
// "/" matches everything. "/api" matches "/api" and "/api/x" but NOT "/apiv2".
func pathPrefixMatches(route, prefix string) bool {
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(route, prefix) {
		return false
	}
	return len(route) == len(prefix) || route[len(prefix)] == '/'
}

func (cp *ClientPolicy) AddRoute(route RoutePolicy) error {
	if err := route.Validate(); err != nil {
		return err
	}
	key := route.Route + "|" + route.Method
	for _, r := range cp.Routes {
		if r.Route+"|"+r.Method == key {
			return fmt.Errorf("%w: route=%q method=%q", ErrDuplicateRoute, route.Route, route.Method)
		}
	}
	cp.Routes = append(cp.Routes, route)
	cp.UpdatedAt = time.Now()
	return nil
}

func (cp *ClientPolicy) RemoveRoute(route, method string) error {
	key := route + "|" + method
	for i, r := range cp.Routes {
		if r.Route+"|"+r.Method == key {
			cp.Routes = append(cp.Routes[:i], cp.Routes[i+1:]...)
			cp.UpdatedAt = time.Now()
			return nil
		}
	}
	return ErrRouteNotFound
}

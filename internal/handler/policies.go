package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

type RoutePolicyResponse struct {
	Route      string `json:"route"`
	RouteType  string `json:"route_type"`
	Method     string `json:"method"`
	Limit      int64  `json:"limit"`
	Window     string `json:"window"`
	Identifier string `json:"identifier"`
}

type ClientPolicyResponse struct {
	ClientID string                `json:"client_id"`
	Routes   []RoutePolicyResponse `json:"routes"`
}

type AllClientsResponse struct {
	Clients []string `json:"clients"`
}

func (h *Handler) GetAllPolicies(c *gin.Context) {
	clients := h.policyMatcher.GetAllClients()
	c.JSON(http.StatusOK, AllClientsResponse{Clients: clients})
}

func (h *Handler) GetPolicies(c *gin.Context) {
	clientID := c.Param("client_id")

	routes := h.policyMatcher.GetPoliciesForClient(clientID)
	if routes == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "client not found"})
		return
	}

	routeResponses := make([]RoutePolicyResponse, len(routes))
	for i, r := range routes {
		routeResponses[i] = toRoutePolicyResponse(r)
	}

	c.JSON(http.StatusOK, ClientPolicyResponse{
		ClientID: clientID,
		Routes:   routeResponses,
	})
}

func toRoutePolicyResponse(r models.RoutePolicy) RoutePolicyResponse {
	return RoutePolicyResponse{
		Route:      r.Route,
		RouteType:  string(r.RouteType),
		Method:     r.Method,
		Limit:      r.Limit,
		Window:     r.Window.String(),
		Identifier: string(r.Identifier),
	}
}

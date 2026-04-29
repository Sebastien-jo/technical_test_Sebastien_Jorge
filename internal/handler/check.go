package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

type CheckRequest struct {
	ClientID  string `json:"client_id" binding:"required"`
	Route     string `json:"route" binding:"required"`
	Method    string `json:"method" binding:"required"`
	SessionID string `json:"session_id"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
}

type CheckResponse struct {
	Allowed    bool   `json:"allowed"`
	Remaining  int64  `json:"remaining"`
	ResetTime  int64  `json:"reset_time"`
	RetryAfter int64  `json:"retry_after,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (h *Handler) Check(c *gin.Context) {
	var req CheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	routePolicy, err := h.policyMatcher.FindPolicy(req.ClientID, req.Route, req.Method)
	if err != nil {
		switch {
		case errors.Is(err, models.ErrClientNotFound),
			errors.Is(err, models.ErrRouteNotFound),
			errors.Is(err, models.ErrPolicyNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
		return
	}

	identifier, err := buildIdentifier(&req, routePolicy.Identifier)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	decision := h.rateLimiter.Check(req.ClientID, routePolicy, identifier)

	tags := []string{"client_id:" + req.ClientID, "route:" + routePolicy.Route}
	if decision.Allowed {
		_ = h.metrics.Incr("decision.allowed", tags, 1)
	} else {
		_ = h.metrics.Incr("decision.denied", tags, 1)
	}

	c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", routePolicy.Limit))
	c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", decision.Remaining))
	c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", decision.ResetTime.Unix()))

	resp := CheckResponse{
		Allowed:   decision.Allowed,
		Remaining: decision.Remaining,
		ResetTime: decision.ResetTime.Unix(),
	}

	if !decision.Allowed {
		retryAfter := int64(decision.RetryAfter / time.Second)
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		resp.RetryAfter = retryAfter
		resp.Message = decision.Message
		c.JSON(http.StatusTooManyRequests, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func buildIdentifier(req *CheckRequest, identType models.IdentifierType) (string, error) {
	switch identType {
	case models.IdentifierSessionID:
		if req.SessionID == "" {
			return "", errors.New("policy requires session_id but it is missing from the request")
		}
		return req.SessionID, nil
	case models.IdentifierIPUserAgent:
		if req.IP == "" || req.UserAgent == "" {
			return "", errors.New("policy requires ip and user_agent but one or both are missing from the request")
		}
		return req.IP + ":" + req.UserAgent, nil
	default:
		return "global", nil
	}
}

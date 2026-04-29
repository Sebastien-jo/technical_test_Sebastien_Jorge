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

	identifier := buildIdentifier(&req, routePolicy.Identifier)
	decision := h.rateLimiter.Check(req.ClientID, routePolicy, identifier)

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

func buildIdentifier(req *CheckRequest, identType models.IdentifierType) string {
	switch identType {
	case models.IdentifierSessionID:
		return req.SessionID
	case models.IdentifierIP:
		return req.IP
	case models.IdentifierIPUserAgent:
		return req.IP + ":" + req.UserAgent
	default:
		return "global"
	}
}

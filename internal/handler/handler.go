package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/sebastien-jorge/rate-limiter/internal/service"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
)

type Handler struct {
	rateLimiter   *service.RateLimiter
	policyMatcher *service.PolicyMatcher
	store         storage.Store
}

func New(rl *service.RateLimiter, pm *service.PolicyMatcher, store storage.Store) *Handler {
	return &Handler{rateLimiter: rl, policyMatcher: pm, store: store}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.POST("/check", h.Check)
	r.GET("/policies/:client_id", h.GetPolicies)
	r.GET("/health", h.Health)
}

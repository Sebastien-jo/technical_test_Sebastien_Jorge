package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type HealthResponse struct {
	Status  string `json:"status"`
	Storage string `json:"storage"`
}

func (h *Handler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	storageStatus := "ok"
	if err := h.store.Health(ctx); err != nil {
		storageStatus = "degraded"
	}

	status := "ok"
	if storageStatus != "ok" {
		status = "degraded"
	}

	c.JSON(http.StatusOK, HealthResponse{
		Status:  status,
		Storage: storageStatus,
	})
}

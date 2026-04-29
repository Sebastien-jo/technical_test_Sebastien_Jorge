package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sebastien-jorge/rate-limiter/internal/handler"
	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/sebastien-jorge/rate-limiter/internal/service"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupRouter builds a test router wired with the given policies.
func setupRouter(policies []*models.ClientPolicy) *gin.Engine {
	rl := service.NewRateLimiter()
	pm := service.NewPolicyMatcher(policies)
	store := storage.NewMemoryStore()
	h := handler.New(rl, pm, store)

	r := gin.New()
	r.Use(handler.RequestID())
	h.RegisterRoutes(r)
	return r
}

func postCheck(r *gin.Engine, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func testPolicy(clientID, route, method string, limit int64, window time.Duration) *models.ClientPolicy {
	return &models.ClientPolicy{
		ClientID: models.ClientID(clientID),
		Routes: []models.RoutePolicy{
			{
				Route:      route,
				RouteType:  models.RouteExact,
				Method:     method,
				Limit:      limit,
				Window:     window,
				Identifier: models.IdentifierNone,
			},
		},
	}
}

func TestCheck_Allowed(t *testing.T) {
	r := setupRouter([]*models.ClientPolicy{
		testPolicy("client-a", "/api/videos", "GET", 10, time.Minute),
	})

	w := postCheck(r, map[string]any{
		"client_id": "client-a",
		"route":     "/api/videos",
		"method":    "GET",
	})

	require.Equal(t, http.StatusOK, w.Code)

	var resp handler.CheckResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Allowed)
	assert.Equal(t, int64(9), resp.Remaining)

	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Limit"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

func TestCheck_RateLimited(t *testing.T) {
	r := setupRouter([]*models.ClientPolicy{
		testPolicy("client-b", "/upload", "POST", 1, time.Minute),
	})

	body := map[string]any{"client_id": "client-b", "route": "/upload", "method": "POST"}

	w1 := postCheck(r, body)
	require.Equal(t, http.StatusOK, w1.Code)

	w2 := postCheck(r, body)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)

	var resp handler.CheckResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.False(t, resp.Allowed)
	assert.NotEmpty(t, resp.Message)
	assert.NotEmpty(t, w2.Header().Get("Retry-After"))
}

func TestCheck_MissingRequiredFields(t *testing.T) {
	r := setupRouter(nil)

	w := postCheck(r, map[string]any{"client_id": "x"}) // missing route and method
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCheck_InvalidJSON(t *testing.T) {
	r := setupRouter(nil)

	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewBufferString("{invalid}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCheck_UnknownClient(t *testing.T) {
	r := setupRouter(nil)

	w := postCheck(r, map[string]any{
		"client_id": "ghost",
		"route":     "/api",
		"method":    "GET",
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCheck_UnknownRoute(t *testing.T) {
	r := setupRouter([]*models.ClientPolicy{
		testPolicy("client-c", "/api/videos", "GET", 10, time.Minute),
	})

	w := postCheck(r, map[string]any{
		"client_id": "client-c",
		"route":     "/api/unknown",
		"method":    "GET",
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCheck_IdentifierIPUserAgent(t *testing.T) {
	policy := &models.ClientPolicy{
		ClientID: "client-d",
		Routes: []models.RoutePolicy{
			{
				Route:      "/stream",
				RouteType:  models.RouteExact,
				Method:     "GET",
				Limit:      2,
				Window:     time.Minute,
				Identifier: models.IdentifierIPUserAgent,
			},
		},
	}
	r := setupRouter([]*models.ClientPolicy{policy})

	body := map[string]any{
		"client_id":  "client-d",
		"route":      "/stream",
		"method":     "GET",
		"ip":         "1.2.3.4",
		"user_agent": "Mozilla/5.0",
	}

	postCheck(r, body)
	postCheck(r, body)
	w := postCheck(r, body)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	body["ip"] = "5.6.7.8"
	w2 := postCheck(r, body)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestGetPolicies_Found(t *testing.T) {
	r := setupRouter([]*models.ClientPolicy{
		testPolicy("client-a", "/api/videos", "GET", 100, time.Minute),
	})

	req := httptest.NewRequest(http.MethodGet, "/policies/client-a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp handler.ClientPolicyResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "client-a", resp.ClientID)
	assert.Len(t, resp.Routes, 1)
	assert.Equal(t, "/api/videos", resp.Routes[0].Route)
	assert.Equal(t, int64(100), resp.Routes[0].Limit)
}

func TestGetPolicies_NotFound(t *testing.T) {
	r := setupRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/policies/nobody", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHealth_OK(t *testing.T) {
	r := setupRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp handler.HealthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "ok", resp.Storage)
}

func TestRequestID_GeneratedWhenAbsent(t *testing.T) {
	r := setupRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEmpty(t, w.Header().Get("X-Trace-ID"))
}

func TestRequestID_PassedThroughWhenPresent(t *testing.T) {
	r := setupRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Trace-ID", "my-trace-id")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "my-trace-id", w.Header().Get("X-Trace-ID"))
}

func TestGetAllPolicies(t *testing.T) {
	r := setupRouter([]*models.ClientPolicy{
		testPolicy("partner", "/api/videos", "GET", 100, time.Minute),
		testPolicy("application", "/api/users", "POST", 10, time.Minute),
	})

	req := httptest.NewRequest(http.MethodGet, "/policies", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp handler.AllClientsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.ElementsMatch(t, []string{"partner", "application"}, resp.Clients)
}

func TestLogger_DoesNotBreakRequests(t *testing.T) {
	rl := service.NewRateLimiter()
	pm := service.NewPolicyMatcher(nil)
	store := storage.NewMemoryStore()
	h := handler.New(rl, pm, store)

	r := gin.New()
	r.Use(handler.RequestID())
	r.Use(handler.Logger())
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

// Standard policies reused across all integration tests.
// Client IDs match config.yaml: partner, application, internal_service, api_consumer.
var testPolicies = []*models.ClientPolicy{
	{
		ClientID: "partner",
		Routes: []models.RoutePolicy{
			{Route: "/api/videos", RouteType: models.RoutePrefix, Method: "GET", Limit: 2000, Window: time.Minute, Identifier: models.IdentifierNone},
			{Route: "/api/content", RouteType: models.RoutePrefix, Method: "*", Limit: 500, Window: time.Minute, Identifier: models.IdentifierNone},
		},
	},
	{
		ClientID: "application",
		Routes: []models.RoutePolicy{
			{Route: "/api/videos", RouteType: models.RoutePrefix, Method: "*", Limit: 100, Window: time.Minute, Identifier: models.IdentifierSessionID},
			{Route: "/api/users", RouteType: models.RouteExact, Method: "GET", Limit: 10, Window: time.Minute, Identifier: models.IdentifierNone},
			{Route: "/api/payment", RouteType: models.RouteExact, Method: "POST", Limit: 5, Window: time.Minute, Identifier: models.IdentifierIPUserAgent},
		},
	},
	{
		ClientID: "internal_service",
		Routes: []models.RoutePolicy{
			{Route: "/", RouteType: models.RoutePrefix, Method: "*", Limit: 10000, Window: time.Minute, Identifier: models.IdentifierNone},
		},
	},
	{
		ClientID: "api_consumer",
		Routes: []models.RoutePolicy{
			{Route: "/api/videos", RouteType: models.RoutePrefix, Method: "*", Limit: 100, Window: time.Minute, Identifier: models.IdentifierIPUserAgent},
			{Route: "/api", RouteType: models.RoutePrefix, Method: "GET", Limit: 300, Window: time.Minute, Identifier: models.IdentifierIPUserAgent},
		},
	},
}

// SetupTestApp creates a fully wired router backed by in-memory storage.
// The returned cleanup func stops the background goroutine inside MemoryStore.
func SetupTestApp() (*gin.Engine, func()) {
	store := storage.NewMemoryStore()
	rl := service.NewRateLimiter()
	pm := service.NewPolicyMatcher(testPolicies)
	h := handler.New(rl, pm, store)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(handler.RequestID())
	h.RegisterRoutes(r)

	return r, store.Close
}

// checkReq is the JSON body sent to POST /check.
type checkReq struct {
	ClientID  string `json:"client_id"`
	Route     string `json:"route"`
	Method    string `json:"method"`
	SessionID string `json:"session_id,omitempty"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// doCheck makes a POST /check request and decodes the response.
func doCheck(r *gin.Engine, req checkReq) (int, *handler.CheckResponse) {
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)
	var resp handler.CheckResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return w.Code, &resp
}

// doGet makes a GET request and returns status + raw body.
func doGet(r *gin.Engine, path string) (int, []byte) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// doGetWithHeader makes a GET request with a custom header.
func doGetWithHeader(r *gin.Engine, path, key, value string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(key, value)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── 1. Basic Functionality ───────────────────────────────────────────────────

func TestCheckEndpointAllow(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, resp := doCheck(r, checkReq{
		ClientID:  "application",
		Route:     "/api/videos",
		Method:    "GET",
		SessionID: "session-001",
	})

	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Allowed)
	assert.Equal(t, int64(99), resp.Remaining)
}

func TestCheckEndpointDenied(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 5; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "1.2.3.4", UserAgent: "ua"})
	}

	code, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "1.2.3.4", UserAgent: "ua"})
	require.Equal(t, http.StatusTooManyRequests, code)
	assert.False(t, resp.Allowed)
	assert.NotEmpty(t, resp.Message)
}

func TestCheckEndpointRetryAfter(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 5; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "10.0.0.1", UserAgent: "ua"})
	}

	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "10.0.0.1", UserAgent: "ua"})
	assert.Greater(t, resp.RetryAfter, int64(0))
}

func TestCheckEndpointResetTime(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "session-rst"})
	assert.GreaterOrEqual(t, resp.ResetTime, time.Now().Unix())
}

// ── 2. Quota Isolation ───────────────────────────────────────────────────────

func TestQuotaSeparatedByClient(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 10; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	}
	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusTooManyRequests, code1, "application should be rate limited")

	code2, _ := doCheck(r, checkReq{ClientID: "partner", Route: "/api/videos", Method: "GET"})
	assert.Equal(t, http.StatusOK, code2, "partner has its own independent bucket")
}

func TestQuotaSeparatedByRoute(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 10; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	}
	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusTooManyRequests, code1)

	code2, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "s1"})
	assert.Equal(t, http.StatusOK, code2, "/api/videos has its own bucket")
}

func TestQuotaSeparatedByIdentifier(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 5; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "1.1.1.1", UserAgent: "ua"})
	}
	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "1.1.1.1", UserAgent: "ua"})
	assert.Equal(t, http.StatusTooManyRequests, code1, "IP 1.1.1.1 exhausted")

	code2, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "2.2.2.2", UserAgent: "ua"})
	assert.Equal(t, http.StatusOK, code2, "IP 2.2.2.2 has its own bucket")
}

func TestQuotaSeparatedByIPIdentifier(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 5; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "10.0.0.1", UserAgent: "ua"})
	}

	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "10.0.0.1", UserAgent: "ua"})
	assert.Equal(t, http.StatusTooManyRequests, code1)

	code2, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "10.0.0.2", UserAgent: "ua"})
	assert.Equal(t, http.StatusOK, code2)
}

func TestExactRouteMatching(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusOK, code1, "/api/users exact route should match")

	// /api/users/123 has no exact match for application and is not under any prefix.
	code2, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users/123", Method: "GET"})
	assert.Equal(t, http.StatusNotFound, code2, "/api/users/123 must not match exact /api/users")
}

func TestPrefixRouteMatching(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	routes := []string{"/api/videos", "/api/videos/123", "/api/videos/456/comments"}
	for _, route := range routes {
		code, _ := doCheck(r, checkReq{ClientID: "application", Route: route, Method: "GET", SessionID: "s1"})
		assert.Equal(t, http.StatusOK, code, "%q should match prefix /api/videos", route)
	}
}

func TestPrefixNotMatchingDifferentPrefix(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	// api_consumer has prefix /api. /apiv2 must NOT be treated as matching /api.
	code, _ := doCheck(r, checkReq{ClientID: "api_consumer", Route: "/apiv2/videos", Method: "GET"})
	assert.Equal(t, http.StatusNotFound, code)
}

func TestWildcardMethod(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for _, method := range []string{"GET", "POST", "DELETE", "PATCH", "PUT"} {
		code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: method, SessionID: "s1"})
		assert.Equal(t, http.StatusOK, code, "method %q should match wildcard *", method)
	}
}

func TestSpecificMethod(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code1, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusOK, code1, "GET should match")

	code2, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "POST"})
	assert.Equal(t, http.StatusNotFound, code2, "POST must not match a GET-only route")
}

func TestMissingClientID(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	raw, _ := json.Marshal(map[string]string{"route": "/api/videos", "method": "GET"})
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMissingRoute(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	raw, _ := json.Marshal(map[string]string{"client_id": "application", "method": "GET"})
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMissingMethod(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	raw, _ := json.Marshal(map[string]string{"client_id": "application", "route": "/api/videos"})
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUnknownClient(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, _ := doCheck(r, checkReq{ClientID: "ghost", Route: "/api/videos", Method: "GET"})
	assert.Equal(t, http.StatusNotFound, code)
}

func TestUnknownRoute(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/does/not/exist", Method: "GET"})
	assert.Equal(t, http.StatusNotFound, code)
}

func TestTraceIDHeader(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, _ := doGet(r, "/health")
	require.Equal(t, http.StatusOK, code)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.NotEmpty(t, w.Header().Get("X-Trace-ID"))
}

func TestTraceIDPassthroughWhenPresent(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	w := doGetWithHeader(r, "/health", "X-Trace-ID", "trace-xyz-123")
	assert.Equal(t, "trace-xyz-123", w.Header().Get("X-Trace-ID"))
}

func TestRateLimitHeaders(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	body, _ := json.Marshal(checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "s-hdrs"})
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Limit"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

func TestRetryAfterHeader(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 5; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "3.3.3.3", UserAgent: "ua"})
	}

	body, _ := json.Marshal(checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "3.3.3.3", UserAgent: "ua"})
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestContentTypeHeader(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "s1"})
	assert.Equal(t, http.StatusOK, code)
}

func TestGetAllClients(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, body := doGet(r, "/policies")
	require.Equal(t, http.StatusOK, code)

	var resp handler.AllClientsResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Contains(t, resp.Clients, "partner")
	assert.Contains(t, resp.Clients, "application")
	assert.Contains(t, resp.Clients, "internal_service")
	assert.Contains(t, resp.Clients, "api_consumer")
}

func TestGetClientPolicies(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, body := doGet(r, "/policies/application")
	require.Equal(t, http.StatusOK, code)

	var resp handler.ClientPolicyResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, "application", resp.ClientID)
	assert.Len(t, resp.Routes, 3)
}

func TestGetUnknownClient(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, _ := doGet(r, "/policies/ghost")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestPoliciesResponseStructure(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, body := doGet(r, "/policies/application")
	require.Equal(t, http.StatusOK, code)

	var resp handler.ClientPolicyResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, route := range resp.Routes {
		assert.NotEmpty(t, route.Route)
		assert.NotEmpty(t, route.RouteType)
		assert.NotEmpty(t, route.Method)
		assert.Greater(t, route.Limit, int64(0))
		assert.NotEmpty(t, route.Window)
		assert.NotEmpty(t, route.Identifier)
	}
}

func TestHealthEndpointSuccessful(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, body := doGet(r, "/health")
	require.Equal(t, http.StatusOK, code)

	var resp handler.HealthResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.Equal(t, "ok", resp.Status)
}

func TestHealthEndpointStructure(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	code, body := doGet(r, "/health")
	require.Equal(t, http.StatusOK, code)

	var resp handler.HealthResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	assert.NotEmpty(t, resp.Status)
	assert.NotEmpty(t, resp.Storage)
}

func TestExactQuotaExhaustion(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	const limit = 10
	for i := 0; i < limit; i++ {
		code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
		require.Equal(t, http.StatusOK, code, "request %d/%d should be allowed", i+1, limit)
	}

	code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusTooManyRequests, code, "request %d should be denied", limit+1)
}

func TestZeroRemaining(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 10; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	}

	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, int64(0), resp.Remaining)
}

func TestImmediateRetryAfter(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 10; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	}

	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Greater(t, resp.RetryAfter, int64(0))
}

func TestConcurrentRequestsToSameBucket(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	const (
		total = 150
		limit = 100 // application /api/videos limit
	)

	var allowed, denied atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := doCheck(r, checkReq{
				ClientID:  "application",
				Route:     "/api/videos",
				Method:    "GET",
				SessionID: "concurrent-bucket",
			})
			if code == http.StatusOK {
				allowed.Add(1)
			} else {
				denied.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(limit), allowed.Load(), "exactly 100 requests should be allowed")
	assert.Equal(t, int64(total-limit), denied.Load(), "exactly 50 requests should be denied")
}

func TestQuotaPersistsAcrossRequests(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	// Consume 4 of 5 tokens.
	for i := 0; i < 4; i++ {
		code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "5.5.5.5", UserAgent: "ua"})
		require.Equal(t, http.StatusOK, code)
	}

	// 5th request: 1 token consumed, 0 remaining.
	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/payment", Method: "POST", IP: "5.5.5.5", UserAgent: "ua"})
	assert.Equal(t, int64(0), resp.Remaining, "last token consumed, 0 left")
}

func TestDifferentSessionsIndependent(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	// Session A consumes 50 tokens.
	for i := 0; i < 50; i++ {
		doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "session-A"})
	}

	// Session B is unaffected — starts with full 100.
	_, resp := doCheck(r, checkReq{ClientID: "application", Route: "/api/videos", Method: "GET", SessionID: "session-B"})
	assert.Equal(t, int64(99), resp.Remaining, "session B should have its own full bucket")
}

func TestBurstBehavior(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	for i := 0; i < 10; i++ {
		code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
		assert.Equal(t, http.StatusOK, code, "burst request %d should be allowed", i+1)
	}

	code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "GET"})
	assert.Equal(t, http.StatusTooManyRequests, code, "bucket should be empty after burst")
}

func TestInvalidJSON(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewBufferString("{not valid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMethodCaseInsensitive(t *testing.T) {
	r, cleanup := SetupTestApp()
	defer cleanup()

	// Our policy matcher uses strings.EqualFold — lowercase "get" should match.
	code, _ := doCheck(r, checkReq{ClientID: "application", Route: "/api/users", Method: "get"})
	assert.Equal(t, http.StatusOK, code)
}

package handler

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sebastien-jorge/rate-limiter/internal/observability"
)

const requestIDKey = "request_id"

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Trace-ID")
		if id == "" {
			id = newRequestID()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Trace-ID", id)
		c.Next()
	}
}

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		args := []any{
			"trace_id", c.GetString(requestIDKey),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		}

		// Correlate with the active Datadog APM span so logs and traces link in the UI.
		if span, ok := ddtracer.SpanFromContext(c.Request.Context()); ok {
			args = append(args,
				"dd.trace_id", span.Context().TraceID(),
				"dd.span_id", span.Context().SpanID(),
			)
		}

		slog.Info("request", args...)
	}
}

// MetricsMiddleware records per-request counters and latency histograms in DogStatsD.
func MetricsMiddleware(m observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		tags := []string{
			"endpoint:" + c.FullPath(),
			fmt.Sprintf("status:%d", c.Writer.Status()),
		}
		_ = m.Incr("http.requests", tags, 1)
		_ = m.Histogram("http.latency_ms", float64(time.Since(start).Milliseconds()), tags, 1)
	}
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

package observability

import "github.com/DataDog/datadog-go/v5/statsd"

// Metrics is the minimal interface for recording service metrics.
// *statsd.Client satisfies it directly; noopMetrics is used when no agent is configured.
type Metrics interface {
	Incr(name string, tags []string, rate float64) error
	Histogram(name string, value float64, tags []string, rate float64) error
	Close() error
}

type noopMetrics struct{}

func (noopMetrics) Incr(_ string, _ []string, _ float64) error                { return nil }
func (noopMetrics) Histogram(_ string, _ float64, _ []string, _ float64) error { return nil }
func (noopMetrics) Close() error                                                { return nil }

// NewNoopMetrics returns a no-op Metrics implementation (used in tests and when
// DD_AGENT_HOST is not set).
func NewNoopMetrics() Metrics { return noopMetrics{} }

// NewMetrics returns a DogStatsD client when addr is non-empty (e.g. "localhost:8125"),
// or a no-op implementation so the service starts cleanly without a Datadog agent.
func NewMetrics(addr string) (Metrics, error) {
	if addr == "" {
		return noopMetrics{}, nil
	}
	return statsd.New(addr, statsd.WithNamespace("rate_limiter."))
}

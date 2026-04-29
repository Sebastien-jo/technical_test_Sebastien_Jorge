package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNoopMetrics(t *testing.T) {
	m := NewNoopMetrics()
	require.NotNil(t, m)
	assert.NoError(t, m.Incr("test.counter", []string{"tag:val"}, 1))
	assert.NoError(t, m.Histogram("test.hist", 42.0, []string{"tag:val"}, 1))
	assert.NoError(t, m.Close())
}

func TestNewMetrics_NoAddr_ReturnsNoop(t *testing.T) {
	m, err := NewMetrics("")
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.NoError(t, m.Incr("x", nil, 1))
	assert.NoError(t, m.Histogram("x", 1, nil, 1))
	assert.NoError(t, m.Close())
}

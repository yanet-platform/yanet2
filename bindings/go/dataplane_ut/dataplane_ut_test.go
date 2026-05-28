package dataplaneut

import (
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessLifecycle exercises construction, shared-memory access, and
// teardown of the Harness without running any packets.
func TestHarnessLifecycle(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	require.NotNil(t, h)
	defer h.Free()

	shm := h.SharedMemory()
	require.NotNil(t, shm)
}

// TestTimeRoundTrip verifies that SetCurrentTime and CurrentTime agree and
// that AdvanceTime correctly accumulates the delta.
func TestTimeRoundTrip(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	defer h.Free()

	epoch := time.Unix(0, 1_000_000_000)
	h.SetCurrentTime(epoch)
	got := h.CurrentTime()
	assert.Equal(t, epoch.UnixNano(), got.UnixNano())

	advanced := h.AdvanceTime(500 * time.Millisecond)
	assert.Equal(t, epoch.Add(500*time.Millisecond).UnixNano(), advanced.UnixNano())
	assert.Equal(t, advanced.UnixNano(), h.CurrentTime().UnixNano())
}

// TestAgentAttach checks that a control-plane agent can be attached to the
// shared-memory arena exposed by the harness.
func TestAgentAttach(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	defer h.Free()

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("smoke-agent", 0, datasize.MB*2)
	require.NoError(t, err)
	require.NotNil(t, agent)
}

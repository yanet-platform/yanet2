package balancer

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	test_utils "github.com/yanet-platform/yanet2/test_utils/go"
)

func TestMain(m *testing.M) {
	mock, _ = test_utils.NewYanetMock(dpMemory, cpMemory, []string{"balancer"})
	if mock == nil {
		panic("failed to create yanet mock")
	}
	ret := m.Run()
	os.Exit(ret)
}

func TestAgentAttach(t *testing.T) {
	for i := range 10 {
		agent := AttachAgent(t)
		require.NotNil(t, agent)
		t.Logf("successfully attached agent %d", i)
	}
}

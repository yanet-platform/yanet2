package balancer

import (
	"os"
	"testing"

	test_utils "github.com/yanet-platform/yanet2/test_utils/go"
)

func TestMain(m *testing.M) {
	mock, _ = test_utils.NewYanetMock(dpMemory, cpMemory, []string{"balancer"})
	if mock == nil {
		panic("failed to create yanet mock")
	}
	agent, _ = mock.AttachAgent("balancer", agentMemory)
	if agent == nil {
		panic("failed to attach agent to yanet mock")
	}
	ret := m.Run()
	os.Exit(ret)
}

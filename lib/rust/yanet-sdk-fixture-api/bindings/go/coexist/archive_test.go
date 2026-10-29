//go:build sdk_rust_fixtures

package coexist_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// Test_Archives_TwoRuntimesWithRegex verifies that two localized runtime copies
// from one fixture API build contain errors and double panics beside librure.
func Test_Archives_TwoRuntimesWithRegex(t *testing.T) {
	path := testshm.NewStorage(t, testshm.StorageTypes{})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	agent, err := memory.AgentAttach("sdk-coexist", 0, 8*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })

	var group errgroup.Group
	for action := range uint64(3) {
		for range 8 {
			group.Go(func() error {
				fault := cfixture.FaultProbeRefusal
				firstMessage, secondMessage := "fixture probe refusal", "tally probe refusal"
				tallyFault := uint64(1)
				if action == 1 {
					fault, tallyFault = cfixture.FaultProbePanic, 3
					firstMessage, secondMessage = "fixture probe panic", "tally probe panic"
				} else if action == 2 {
					fault, tallyFault = cfixture.FaultPanicPayload, 2
					firstMessage, secondMessage = "panic:", "panic:"
				}
				spec := cfixture.NewFixtureSpec(cfixture.ModePass, 0, fault)
				_, first := cfixture.NewFixtureConfig(agent, "fixture", "hits", "port0", &spec, nil)
				_, second := cfixture.NewTallyConfigWithFault(agent, "tally", 7, tallyFault)
				if first == nil || second == nil {
					return fmt.Errorf("action %d escaped error containment: %v, %v", action, first, second)
				}
				if !strings.Contains(first.Error(), firstMessage) || !strings.Contains(second.Error(), secondMessage) {
					return fmt.Errorf("action %d did not reach both authored methods: %v, %v", action, first, second)
				}
				_, err := agent.DPConfig().CountersByTags(nil, []string{"^(v0|v1|v2|held)$"})
				if err != nil {
					return err
				}
				_, err = agent.DPConfig().CountersByTags(nil, []string{"("})
				if err == nil {
					return fmt.Errorf("invalid regex was accepted")
				}
				return nil
			})
		}
	}
	require.NoError(t, group.Wait())
}

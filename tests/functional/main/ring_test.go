package functional

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ringInfo is one ring as the ring CLI prints it in JSON.
type ringInfo struct {
	Name         string `json:"name"`
	Capacity     uint64 `json:"capacity"`
	PublishBatch uint32 `json:"publish_batch"`
}

// ringCLI runs the ring CLI with the given arguments in the guest.
func ringCLI(fw *framework.TestFramework, args string) (string, error) {
	return fw.ExecuteCommand(framework.CLIRing + " " + args)
}

// listRings returns every registered ring from the JSON list output.
func listRings(t *testing.T, fw *framework.TestFramework) []ringInfo {
	t.Helper()
	output, err := ringCLI(fw, "list --format json")
	require.NoError(t, err, "ring list failed")
	var rings []ringInfo
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &rings),
		"ring list must be a JSON array: %q", output)
	return rings
}

// showRing returns one ring from the JSON show output.
func showRing(t *testing.T, fw *framework.TestFramework, name string) ringInfo {
	t.Helper()
	output, err := ringCLI(fw, "show --format json --name "+name)
	require.NoError(t, err, "ring show failed")
	var response struct {
		Ring *ringInfo `json:"ring"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &response),
		"ring show must be a JSON object: %q", output)
	require.NotNil(t, response.Ring, "ring show must carry the ring")
	return *response.Ring
}

// findRing returns the listed ring with the given name.
//
// The second result is false when no listed ring has that name.
func findRing(rings []ringInfo, name string) (ringInfo, bool) {
	for _, ring := range rings {
		if ring.Name == name {
			return ring, true
		}
	}
	return ringInfo{}, false
}

// Test_RingCLI_Lifecycle checks that the ring CLI drives the ring service
// that a running pdump module hosts.
//
// A created ring appears in list and show with its capacity and publish
// batch. A refused create reports its gRPC code in JSON and leaves the
// registry unchanged. A deleted ring is gone from list and show.
func Test_RingCLI_Lifecycle(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testRingCLILifecycle(t, fw)
	})
}

func testRingCLILifecycle(t *testing.T, fw *framework.TestFramework) {
	const (
		ringName     = "ring-tfn0"
		capacity     = uint64(64 << 10)
		publishBatch = uint32(32)
	)

	// Remove the test ring from the shared VM after a failure.
	//
	// A failed step may leave the ring behind. Later tests need an empty
	// registry. A delete of a ring that does not exist just fails, and that
	// is fine here.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		_, _ = ringCLI(fw, "delete --name "+ringName)
	})

	fw.Run("Create_lists_and_shows_ring", func(fw *framework.TestFramework, t *testing.T) {
		_, listed := findRing(listRings(t, fw), ringName)
		require.False(t, listed, "ring must not exist before create")

		_, err := ringCLI(fw, fmt.Sprintf(
			"create --name %s --capacity %d --publish-batch %d",
			ringName, capacity, publishBatch,
		))
		require.NoError(t, err, "ring create failed")

		want := ringInfo{Name: ringName, Capacity: capacity, PublishBatch: publishBatch}
		ring, listed := findRing(listRings(t, fw), ringName)
		require.True(t, listed, "created ring must be listed")
		require.Equal(t, want, ring)
		require.Equal(t, want, showRing(t, fw, ringName))
	})

	fw.Run("Duplicate_create_changes_nothing", func(fw *framework.TestFramework, t *testing.T) {
		before := listRings(t, fw)

		output, err := ringCLI(fw, fmt.Sprintf("--format json create --name %s --capacity %d", ringName, capacity))
		require.Error(t, err, "duplicate create must exit non-zero")
		var failure struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &failure),
			"duplicate create must report a JSON error: %q", output)
		require.False(t, failure.OK)
		require.Equal(t, "AlreadyExists", failure.Error.Code)
		require.ElementsMatch(t, before, listRings(t, fw), "duplicate create must not change the registry")
	})

	fw.Run("Delete_removes_ring", func(fw *framework.TestFramework, t *testing.T) {
		_, err := ringCLI(fw, "delete --name "+ringName)
		require.NoError(t, err, "ring delete failed")

		_, listed := findRing(listRings(t, fw), ringName)
		require.False(t, listed, "deleted ring must not be listed")
		_, err = ringCLI(fw, "show --name "+ringName)
		require.Error(t, err, "show of a deleted ring must exit non-zero")
	})
}

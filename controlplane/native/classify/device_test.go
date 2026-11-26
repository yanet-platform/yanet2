//go:build linux && (amd64 || arm64)

package classify_test

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// randomDeviceRules returns count reproducible device rules over a few
// dozen devices: lists with repeats, and absent, empty and repeated
// rules.
func randomDeviceRules(seed uint64, count int) []classify.DeviceRule {
	random := rand.New(rand.NewPCG(seed, seed^0x2545f491))
	rules := make([]classify.DeviceRule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.DeviceRule{Absent: true}
		case shape == 1:
			rules[idx] = classify.DeviceRule{}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			ids := make([]uint32, 1+random.IntN(4))
			for idIdx := range ids {
				ids[idIdx] = random.Uint32N(40)
			}
			rules[idx] = classify.DeviceRule{IDs: ids}
		}
	}
	return rules
}

// requireDeviceMatchesReference compiles and commits the rules and
// requires every device's class to reach, through the stage, exactly
// the rules matching the device.
func requireDeviceMatchesReference(t *testing.T, rules []classify.DeviceRule) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrDevice](memory)
	devices, err := classify.CompileDevice(rules)
	require.NoError(t, err)
	require.NoError(t, devices.Commit(attr, &memory.Context, "device"))

	classes := classifytest.Values(&attr.Line)
	ids := make([]uint32, len(classes))
	for id := range ids {
		ids[id] = uint32(id)
	}
	err = classifytest.CheckStage(
		devices.Stage(),
		ids,
		func(id uint32) uint32 { return classes[id] },
		func(rule int, id uint32) bool { return classifytest.DeviceMatches(rules[rule], id) },
	)
	require.NoError(t, err)
}

// Test_CompileDevice_MatchesReference verifies that every device up to
// the largest a rule names gets a class whose stage reaches exactly the
// rules matching the device.
func Test_CompileDevice_MatchesReference(t *testing.T) {
	for seed := range uint64(32) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			requireDeviceMatchesReference(t, randomDeviceRules(seed, int(seed)))
		})
	}
}

// Test_CompileDevice_RefusesIdentifiersPastTheLargestLine verifies that a
// device whose value line would not fit the largest shared block is
// refused as an invalid rule before anything is allocated.
func Test_CompileDevice_RefusesIdentifiersPastTheLargestLine(t *testing.T) {
	cases := []struct {
		name string
		id   uint32
	}{
		{name: "the first identifier past the largest line", id: uint32(shmalloc.MaxSize / 4)},
		{name: "the largest identifier", id: ^uint32(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := classify.CompileDevice([]classify.DeviceRule{{IDs: []uint32{1}}, {IDs: []uint32{tc.id}}})
			require.ErrorIs(t, err, classify.ErrInvalidRule)
		})
	}
}

// Test_DeviceCommit_ExhaustedLeavesAttributeZeroed verifies that a device
// commit refused any of its allocations fails as exhausted, leaves the
// attribute zeroed and gives the allocator back every byte it took.
func Test_DeviceCommit_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the values", kept: []uintptr{unsafe.Sizeof(layout.MemoryContext{})}},
	}
	devices, err := classify.CompileDevice(randomDeviceRules(1, 16))
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			attr := classifytest.Alloc[layout.ClassifyAttrDevice](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			require.ErrorIs(t, devices.Commit(attr, &memory.Context, "device"), shmalloc.ErrNoMemory)
			requireZeroed(t, attr, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
		})
	}
}

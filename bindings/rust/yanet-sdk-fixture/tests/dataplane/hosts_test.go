//go:build sdk_rust_fixtures

package dataplane_test

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// buildHosts builds a hosts configuration named hosts of the hosts over the
// agent, without registering it for cleanup.
func buildHosts(owner *agent.Agent, hosts []sdkfixture.Host) (*moduleConfig, error) {
	module, err := sdkfixture.NewHostsConfig(owner, "hosts", hosts)
	return moduleHandle(module, err, sdkfixture.FreeHostsConfig)
}

// hostsOf returns count hosts of 10.0.0.0/16, host n at 10.0.n/256.n%256
// counted by "host n".
func hostsOf(count int) []sdkfixture.Host {
	hosts := make([]sdkfixture.Host, 0, count)
	for idx := range count {
		hosts = append(hosts, sdkfixture.Host{
			Addr:    netip.AddrFrom4([4]byte{10, 0, byte(idx / 256), byte(idx % 256)}),
			Counter: fmt.Sprintf("host %d", idx),
		})
	}
	return hosts
}

// Test_Hosts_Lookup_CountsEachHostByItsPosition verifies that every host of
// an index of one, three and three hundred hosts is found and counted by its
// own counter, and that an address the index lacks is dropped and counted by
// none.
func Test_Hosts_Lookup_CountsEachHostByItsPosition(t *testing.T) {
	for _, count := range []int{1, 3, 300} {
		t.Run(fmt.Sprintf("%d hosts", count), func(t *testing.T) {
			bed := newBed(t)
			hosts := hostsOf(count)
			config, err := buildHosts(bed.agent, hosts)
			require.NoError(t, err)
			bed.freeAtEnd(config)
			inst := &instance{moduleType: "hosts", name: "hosts", config: config}
			bed.serveBeforeForward(inst)
			frames := make([][]byte, 0, count+2)
			for _, host := range hosts {
				frames = append(frames, ip4Frame([4]byte{192, 0, 2, 1}, host.Addr.As4(), protoUDP, 53, 0))
			}
			missing := [][]byte{
				ip4Frame([4]byte{192, 0, 2, 1}, [4]byte{10, 0, 255, 255}, protoUDP, 53, 0),
				ip4Frame([4]byte{192, 0, 2, 1}, [4]byte{0, 0, 0, 0}, protoUDP, 53, 0),
			}

			result := bed.run(0, append(frames, missing...)...)

			require.ElementsMatch(t, frames, result.Output)
			require.ElementsMatch(t, missing, result.Drop)
			counters := bed.counters(pathTags(inst, "module")...)
			for idx, host := range hosts {
				require.Equal(t, packetsAndBytes(1, len(frames[idx])), valuesOf(t, counters, host.Counter)[0], "%s", host.Addr)
			}
		})
	}
}

// homeSlot returns the slot the walk of an address starts at in an index of
// that many slots, as the lookup reads it: the high bits of the address
// times the Fibonacci multiplier, as many as index the slots.
func homeSlot(addr netip.Addr, slots int) int {
	key := addr.As4()
	return int(uint64(binary.BigEndian.Uint32(key[:])) * 0x9e3779b97f4a7c15 >> (64 - bits.TrailingZeros(uint(slots))))
}

// addrsAt returns the first count addresses of 10.0.0.0/16 whose walks start
// at the home slot in an index of that many slots.
func addrsAt(home, slots, count int) []netip.Addr {
	var addrs []netip.Addr
	for idx := 0; len(addrs) < count; idx++ {
		addr := netip.AddrFrom4([4]byte{10, 0, byte(idx / 256), byte(idx % 256)})
		if homeSlot(addr, slots) == home {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// Test_Hosts_Lookup_CollidingHosts_ProbePastTheirHomeSlot verifies that hosts
// sharing the last slot of the index as their home slot are found where
// their walk places them, the later ones past the end of the index at its
// first slots, and that a missing address whose home slot a host took, the
// last or a first one, walks on to a free slot and is dropped.
//
// Three hosts take an index of eight slots.
func Test_Hosts_Lookup_CollidingHosts_ProbePastTheirHomeSlot(t *testing.T) {
	const slots = 8
	colliding := addrsAt(slots-1, slots, 4)
	hosts := make([]sdkfixture.Host, 0, 3)
	for idx, addr := range colliding[:3] {
		hosts = append(hosts, sdkfixture.Host{Addr: addr, Counter: fmt.Sprintf("host %d", idx)})
	}
	bed := newBed(t)
	config, err := buildHosts(bed.agent, hosts)
	require.NoError(t, err)
	bed.freeAtEnd(config)
	inst := &instance{moduleType: "hosts", name: "hosts", config: config}
	bed.serveBeforeForward(inst)
	frames := make([][]byte, 0, len(hosts))
	for _, host := range hosts {
		frames = append(frames, ip4Frame([4]byte{192, 0, 2, 1}, host.Addr.As4(), protoUDP, 53, 0))
	}
	missing := [][]byte{
		ip4Frame([4]byte{192, 0, 2, 1}, colliding[3].As4(), protoUDP, 53, 0),
		ip4Frame([4]byte{192, 0, 2, 1}, addrsAt(0, slots, 1)[0].As4(), protoUDP, 53, 0),
	}

	result := bed.run(0, append(frames, missing...)...)

	require.ElementsMatch(t, frames, result.Output)
	require.ElementsMatch(t, missing, result.Drop)
	counters := bed.counters(pathTags(inst, "module")...)
	for idx, host := range hosts {
		require.Equal(t, packetsAndBytes(1, len(frames[idx])), valuesOf(t, counters, host.Counter)[0], "%s", host.Addr)
	}
}

// Test_Hosts_Build_EachAllocationRefused_ReturnsMemoryToBaseline verifies
// that a build refused at any of its allocations, that of its index
// included, refuses as exhausted and gives back all it took.
func Test_Hosts_Build_EachAllocationRefused_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)

	sweepAllocations(t, bed.agent, func(owner *agent.Agent) (func() error, error) {
		config, err := buildHosts(owner, hostsOf(3))
		if err != nil {
			return nil, err
		}
		return config.Free, nil
	})
}

// Test_Hosts_Free_Unpublished_ReturnsMemoryToBaseline verifies that freeing
// a configuration no generation holds gives back its index too.
func Test_Hosts_Free_Unpublished_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	config, err := buildHosts(bed.agent, hostsOf(300))
	require.NoError(t, err)

	require.NoError(t, config.Free())

	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

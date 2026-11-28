//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// hostsType is the dataplane module a hosts configuration configures.
const hostsType = "hosts"

// freeSlot is the position of an index slot that holds no key.
const freeSlot = math.MaxUint32

// fibonacci is the multiplier of the index's Fibonacci hashing: the high
// bits of the product depend on every bit of the key, so keys that differ
// only in their low bits, as the addresses of one prefix do, spread over
// the whole index.
const fibonacci = 0x9e3779b97f4a7c15

// Host is a destination of a hosts configuration: its IPv4 address and
// the name of the counter of its packets.
type Host struct {
	Addr    netip.Addr
	Counter string
}

// NewHostsConfig builds a hosts configuration named name in the agent's
// memory: a counter per host, then the index of their addresses, each
// naming the position of its host.
//
// A name its field cannot hold, an address that is not IPv4, one given
// twice and an index past the largest block are refused before anything
// is allocated. A failure frees what the build took; an exhausted agent
// fails with the allocator's out-of-memory error.
func NewHostsConfig(owner *agent.Agent, name string, hosts []Host) (*layout.CpModule, error) {
	if err := checkHosts(hosts); err != nil {
		return nil, fmt.Errorf("hosts of module %q: %w", name, err)
	}
	build := func(config *moduleBlock[HostsConfig]) error {
		return buildHosts(config, hosts)
	}
	return newModule(owner, hostsType, name, build, destroyHosts)
}

// FreeHostsConfig frees a hosts configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeHostsConfig(module *layout.CpModule) error {
	return freeModule(module, destroyHosts)
}

// checkHosts refuses an address that is not IPv4, an address given twice,
// which the index cannot tell apart, and slots past the largest block.
func checkHosts(hosts []Host) error {
	positions := map[netip.Addr]int{}
	for idx, host := range hosts {
		if !host.Addr.Is4() {
			return fmt.Errorf("host %d: %s is not an IPv4 address: %w", idx, host.Addr, ErrInvalidArgument)
		}
		if first, ok := positions[host.Addr]; ok {
			return fmt.Errorf("hosts %d and %d: %s given twice: %w", first, idx, host.Addr, ErrInvalidArgument)
		}
		positions[host.Addr] = idx
	}
	if slotsFor(len(hosts))*uint64(unsafe.Sizeof(HashIndexSlot{})) > uint64(shmalloc.MaxSize) {
		return fmt.Errorf("an index of %d hosts, past the largest block: %w", len(hosts), ErrInvalidArgument)
	}
	return nil
}

// buildHosts builds the body in its order: the counters, then the index.
func buildHosts(config *moduleBlock[HostsConfig], hosts []Host) error {
	module := &config.Header
	body := &config.Body
	counters, err := newSlice(&module.Entity.MemoryContext, &body.Counters, uint64(len(hosts)))
	if err != nil {
		return fmt.Errorf("counters: %w", err)
	}
	for idx, host := range hosts {
		counters[idx].ID, err = cpconfig.RegisterCounter(&module.CounterRegistry, host.Counter, trafficValues)
		if err != nil {
			return fmt.Errorf("counter of host %d: %w", idx, err)
		}
	}
	if err := buildHashIndex(&body.Hosts, &module.Entity.MemoryContext, hosts); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	return nil
}

// buildHashIndex places the address of each host in the index, naming the
// host's position, as the SDK builds a hash index field: the slots in a
// block of a memory context of the index's own, under the parent.
//
// No hosts leave the index zeroed, holding no memory context. A failure
// leaves the index zeroed and its context closed.
func buildHashIndex(index *HashIndex, parent *layout.MemoryContext, hosts []Host) error {
	*index = HashIndex{}
	if len(hosts) == 0 {
		return nil
	}
	shmalloc.ContextInitFrom(&index.MemoryContext, parent, "hash_index")
	slots, ok := shmalloc.AllocArray[HashIndexSlot](&index.MemoryContext, slotsFor(len(hosts)))
	if !ok {
		shmalloc.ContextFini(&index.MemoryContext)
		*index = HashIndex{}
		return shmalloc.ErrNoMemory
	}
	for idx := range slots {
		slots[idx] = HashIndexSlot{Position: freeSlot}
	}
	for position, host := range hosts {
		place(slots, host.Addr.As4(), uint32(position))
	}
	index.Slots.Ptr.Set(&slots[0])
	index.Slots.Len = uint64(len(slots))
	return nil
}

// slotsFor returns the slots of an index of that many keys: a power of
// two that leaves at least half of them free.
func slotsFor(keys int) uint64 {
	return uint64(1) << bits.Len64(uint64(2*keys)-1)
}

// place puts the key in the first free slot of its walk: from its home
// slot on, wrapping. Keys are distinct and the slots at least twice as
// many, so the walk meets a free one.
func place(slots []HashIndexSlot, key [4]byte, position uint32) {
	mask := uint64(len(slots) - 1)
	for at := home(key, len(slots)); ; at = (at + 1) & mask {
		if slots[at].Position == freeSlot {
			slots[at] = HashIndexSlot{Key: key, Position: position}
			return
		}
	}
}

// home returns the first slot of a key's walk in an index of that many
// slots: the high bits of the key's bits times the Fibonacci multiplier,
// as many as index the slots.
func home(key [4]byte, slots int) uint64 {
	shift := 64 - bits.TrailingZeros(uint(slots))
	if shift == 64 {
		return 0
	}
	return uint64(binary.BigEndian.Uint32(key[:])) * fibonacci >> shift
}

// destroyHosts frees what the body holds, field by field, then the header
// and the block.
func destroyHosts(config *moduleBlock[HostsConfig]) {
	body := &config.Body
	if slots := body.Hosts.Slots.Ptr.Get(); slots != nil {
		shmalloc.FreeArray(&body.Hosts.MemoryContext, unsafe.Slice(slots, body.Hosts.Slots.Len))
		shmalloc.ContextFini(&body.Hosts.MemoryContext)
	}
	freeSlice(&config.Header.Entity.MemoryContext, &body.Counters)
	finiModule(config)
}

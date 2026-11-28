//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// tallyType is the dataplane module a tally configuration configures.
const tallyType = "tally"

// parityValues is the count of values of the counter by parity of a tally
// configuration: the packets and bytes of an even first byte, then of an
// odd one.
const parityValues = 2 * trafficValues

// NewTallyConfig builds a tally configuration named name over word in
// the agent's memory.
//
// Its counters are registered in the order held, v2, v0, v1, an order
// other than the fields', and then traffic, parity and running, with no
// packet seen on any worker. Its tagged counters are even of the registry
// routes, the held one, registered first, and then odd and even of the
// registry rules, the counters by parity: a registry order and a counter
// order other than the fields', and one name in two registries. The
// counters picked by id are those registered last in the registry lanes
// as wide, narrow and wide again: two counters, ids 0 and 1. Without
// lanes, that registry is created without counters.
//
// A name its field cannot hold is refused before anything is allocated.
// A failure frees what the build took; an exhausted agent fails with the
// allocator's out-of-memory error.
func NewTallyConfig(owner *agent.Agent, name string, word uint64, withLanes bool) (*layout.CpModule, error) {
	build := func(config *moduleBlock[TallyConfig]) error {
		return buildTally(config, owner.Instance.Dp.WorkerCount, word, withLanes)
	}
	return newModule(owner, tallyType, name, build, destroyTally)
}

// FreeTallyConfig frees a tally configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeTallyConfig(module *layout.CpModule) error {
	return freeModule(module, destroyTally)
}

// tallyCounter is a counter a tally build registers: in the registry of
// its tag, the module's own for none, under its name, of its values, into
// its field.
type tallyCounter struct {
	Tag    string
	Name   string
	Values uint64
	ID     *uint64
	// Registry takes the index of the tag's registry.
	Registry *uint64
}

// buildTally builds the body in its order: the counters, the registry of
// lanes and its counters, then the slot per worker.
func buildTally(config *moduleBlock[TallyConfig], workers, word uint64, withLanes bool) error {
	module := &config.Header
	body := &config.Body
	body.Word = word
	counters := []tallyCounter{
		{"routes", "even", countValues, &body.TaggedHeld.ID, &body.TaggedHeld.Registry},
		{"", "held", countValues, &body.Held.ID, nil},
		{"", "v2", countValues, &body.ByValue[2].ID, nil},
		{"rules", "odd", trafficValues, &body.ByParity[1].ID, &body.ByParity[1].Registry},
		{"", "v0", countValues, &body.ByValue[0].ID, nil},
		{"", "v1", countValues, &body.ByValue[1].ID, nil},
		{"rules", "even", trafficValues, &body.ByParity[0].ID, &body.ByParity[0].Registry},
		{"", "traffic", trafficValues, &body.Traffic.ID, nil},
		{"", "parity", parityValues, &body.Parity.ID, nil},
		{"", "running", countValues, &body.Running.ID, nil},
	}
	for _, counter := range counters {
		registry := &module.CounterRegistry
		if counter.Tag != "" {
			var err error
			registry, *counter.Registry, err = cpconfig.ModuleCounterRegistry(module, counter.Tag)
			if err != nil {
				return fmt.Errorf("registry %q: %w", counter.Tag, err)
			}
		}
		id, err := cpconfig.RegisterCounter(registry, counter.Name, counter.Values)
		if err != nil {
			return fmt.Errorf("counter %q of registry %q: %w", counter.Name, counter.Tag, err)
		}
		*counter.ID = id
	}
	if err := buildLanes(module, &body.ById, withLanes); err != nil {
		return err
	}
	if err := newSlots(&module.Entity.MemoryContext, &body.Seen, workers, 0); err != nil {
		return fmt.Errorf("worker slots: %w", err)
	}
	return nil
}

// buildLanes creates the registry lanes and, with lanes, registers wide,
// narrow and wide again in it: as many counters as distinct names.
func buildLanes(module *layout.CpModule, field *TaggedCounters, withLanes bool) error {
	registry, index, err := cpconfig.ModuleCounterRegistry(module, "lanes")
	if err != nil {
		return fmt.Errorf("registry lanes: %w", err)
	}
	field.Registry = index
	if !withLanes {
		return nil
	}
	for _, name := range []string{"wide", "narrow", "wide"} {
		id, err := cpconfig.RegisterCounter(registry, name, trafficValues)
		if err != nil {
			return fmt.Errorf("counter %q of registry lanes: %w", name, err)
		}
		field.Len = max(field.Len, id+1)
	}
	return nil
}

// destroyTally frees the slot per worker, then the header and the block.
func destroyTally(config *moduleBlock[TallyConfig]) {
	freeSlots(&config.Header.Entity.MemoryContext, &config.Body.Seen)
	finiModule(config)
}

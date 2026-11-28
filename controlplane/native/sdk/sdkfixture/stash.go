//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// stashType is the dataplane module a stash configuration configures.
const stashType = "stash"

// NewStashConfig builds a stash configuration named name in the agent's
// memory: a link to the state object named state, which a generation must
// hold when the configuration is published, and the counters seen and
// filled.
//
// A name its field cannot hold, the state's included, is refused before
// anything is allocated. A failure frees what the build took; an
// exhausted agent fails with the allocator's out-of-memory error.
func NewStashConfig(owner *agent.Agent, name, state string) (*layout.CpModule, error) {
	if err := cpconfig.CheckObjectNames(StateType, state); err != nil {
		return nil, err
	}
	build := func(config *moduleBlock[StashConfig]) error {
		return buildStash(config, state)
	}
	return newModule(owner, stashType, name, build, finiModule[StashConfig])
}

// FreeStashConfig frees a stash configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeStashConfig(module *layout.CpModule) error {
	return freeModule(module, finiModule[StashConfig])
}

// buildStash builds the body in its order: the link, then the counters.
func buildStash(config *moduleBlock[StashConfig], state string) error {
	module := &config.Header
	body := &config.Body
	var err error
	if body.State.Index, err = cpconfig.LinkObject(module, StateType, state); err != nil {
		return fmt.Errorf("state %q: %w", state, err)
	}
	if body.Seen.ID, err = cpconfig.RegisterCounter(&module.CounterRegistry, "seen", countValues); err != nil {
		return fmt.Errorf("counter seen: %w", err)
	}
	if body.Filled.ID, err = cpconfig.RegisterCounter(&module.CounterRegistry, "filled", countValues); err != nil {
		return fmt.Errorf("counter filled: %w", err)
	}
	return nil
}

// LinkedObject is an object a configuration links, by type and name.
type LinkedObject struct {
	Type string
	Name string
}

// PublishedSeed returns the seed of the state object the stash
// configuration named name links, as the generation the instance
// publishes holds both.
//
// A configuration the generation does not hold is not found.
func PublishedSeed(instance *zone.Instance, name string) (uint64, error) {
	var seed uint64
	err := readStash(instance, name, func(generation *zone.Generation, stash *moduleBlock[StashConfig]) {
		seed = linkedState(generation, stash).Seed
	})
	return seed, err
}

// PublishedValues writes the values of the slots of that state object
// into values, as StateValues does, and returns the number of slots.
func PublishedValues(instance *zone.Instance, name string, values []uint64) (uint64, error) {
	var count uint64
	err := readStash(instance, name, func(generation *zone.Generation, stash *moduleBlock[StashConfig]) {
		count = readSlots(linkedState(generation, stash), values)
	})
	return count, err
}

// PublishedLinks returns the objects the stash configuration named name
// links in the generation the instance publishes, in link order.
func PublishedLinks(instance *zone.Instance, name string) ([]LinkedObject, error) {
	var objects []LinkedObject
	err := readStash(instance, name, func(_ *zone.Generation, stash *moduleBlock[StashConfig]) {
		for _, link := range moduleLinks(&stash.Header) {
			objects = append(objects, LinkedObject{Type: shm.String(link.Type[:]), Name: shm.String(link.Name[:])})
		}
	})
	return objects, err
}

// readStash runs the read over the stash configuration named name of the
// generation the instance publishes, under a pin of that generation.
func readStash(
	instance *zone.Instance,
	name string,
	read func(generation *zone.Generation, stash *moduleBlock[StashConfig]),
) error {
	generation, err := instance.Pin()
	if err != nil {
		return err
	}
	defer generation.Release()
	module := generation.LookupModule(stashType, name)
	if module == nil {
		return fmt.Errorf("stash module %q: %w", name, cpconfig.ErrNotFound)
	}
	read(generation, (*moduleBlock[StashConfig])(unsafe.Pointer(module)))
	return nil
}

// linkedState returns the state object a published stash configuration
// links, as the generation holds it: a generation publishes no module
// whose linked object it lacks.
func linkedState(generation *zone.Generation, stash *moduleBlock[StashConfig]) *StateConfig {
	link := moduleLinks(&stash.Header)[stash.Body.State.Index]
	return stateOf(generation.LookupObject(shm.String(link.Type[:]), shm.String(link.Name[:])))
}

// moduleLinks returns the objects a module links, in link order.
func moduleLinks(module *layout.CpModule) []layout.CpModuleObject {
	return registry.Slice(module.Objects.Get(), module.ObjectCount)
}

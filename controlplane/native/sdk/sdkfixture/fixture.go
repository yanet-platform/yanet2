//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// fixtureType is the dataplane module a fixture configuration configures.
const fixtureType = "fixture"

// fixtureWeights is the count of the weights of a fixture configuration,
// which hold 1, 2 and 3.
const fixtureWeights = 3

// Fixture is what a fixture configuration is built from.
type Fixture struct {
	// Mode is a FixtureMode constant: what the module does with a batch.
	Mode uint64
	// Arg is the argument some modes read.
	Arg uint64
	// Counter and Device name the counter and the device the configuration
	// registers.
	Counter string
	Device  string
	// Routes are the ranges of its prefix tree, inserted in order.
	Routes []Range4
}

// NewFixtureConfig builds a fixture configuration named name in the
// agent's memory: the weights 1, 2 and 3, the counter, the device and the
// prefix tree of the routes.
//
// A name its field cannot hold and a range that ends before it starts are
// refused before anything is allocated. A failure frees what the build
// took; an exhausted agent fails with the allocator's out-of-memory error.
func NewFixtureConfig(owner *agent.Agent, name string, fixture Fixture) (*layout.CpModule, error) {
	if err := checkRanges(fixture.Routes); err != nil {
		return nil, fmt.Errorf("routes of fixture module %q: %w", name, err)
	}
	build := func(config *moduleBlock[Config]) error {
		return buildFixture(config, fixture)
	}
	return newModule(owner, fixtureType, name, build, destroyFixture)
}

// FreeFixtureConfig frees a fixture configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeFixtureConfig(module *layout.CpModule) error {
	return freeModule(module, destroyFixture)
}

// buildFixture builds the body in its order: the weights, the counter,
// the device, then the prefix tree.
func buildFixture(config *moduleBlock[Config], fixture Fixture) error {
	context := &config.Header.Entity.MemoryContext
	body := &config.Body
	body.Mode = fixture.Mode
	body.Arg = fixture.Arg
	weights, err := newSlice(context, &body.Weights, fixtureWeights)
	if err != nil {
		return fmt.Errorf("weights: %w", err)
	}
	for idx := range weights {
		weights[idx] = uint64(idx) + 1
	}
	body.Counter.ID, err = cpconfig.RegisterCounter(&config.Header.CounterRegistry, fixture.Counter, 1)
	if err != nil {
		return fmt.Errorf("counter: %w", err)
	}
	body.Device.Index, err = cpconfig.LinkDevice(&config.Header, fixture.Device)
	if err != nil {
		return fmt.Errorf("device: %w", err)
	}
	if err := buildLpm(&body.Routes, context, fixture.Routes); err != nil {
		return fmt.Errorf("routes: %w", err)
	}
	return nil
}

// destroyFixture frees what the body holds, field by field, then the
// header and the block.
func destroyFixture(config *moduleBlock[Config]) {
	body := &config.Body
	freeLpm(&body.Routes)
	freeSlice(&config.Header.Entity.MemoryContext, &body.Weights)
	finiModule(config)
}

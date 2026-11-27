//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// ChainModuleConfig names a module configuration a chain runs.
type ChainModuleConfig struct {
	Type string
	Name string
}

// ChainConfig is a chain: its name and the modules it runs, in order.
type ChainConfig struct {
	Name    string
	Modules []ChainModuleConfig
}

// FunctionChainConfig is a chain of a function with the weight its
// packets are spread by.
type FunctionChainConfig struct {
	Chain  ChainConfig
	Weight uint64
}

// FunctionConfig is a function: its name and its weighted chains.
type FunctionConfig struct {
	Name   string
	Chains []FunctionChainConfig
}

// UpdateFunctions publishes a generation in which the functions replace
// those of their names, as cp_config_update_functions does.
//
// It returns once every worker runs the generation, with no timeout, as C
// does. A name a field cannot hold whole and a chain weight past the
// bound are refused before the instance is touched; a function named
// twice is refused before the copy, and one a device runs with a chain
// running a module the generation does not publish fails the contexts'
// build. Each function continues the counters of the one it replaces, and
// each chain those of the replaced function's chain of its name. A
// refused update publishes nothing.
func UpdateFunctions(instance *zone.Instance, functions []FunctionConfig, options ...UpdateOption) error {
	names := make([]string, 0, len(functions))
	for _, function := range functions {
		if err := checkFunction(function); err != nil {
			return err
		}
		names = append(names, function.Name)
	}
	return publish(instance, options, change{
		Check: func(*layout.CpConfigGen) error {
			return refuseRepeated("function", names)
		},
		Put: func(m *build) error {
			for _, config := range functions {
				function, err := m.newFunction(config)
				if err != nil {
					return fmt.Errorf("function %q: %w", config.Name, err)
				}
				if err := m.upsertFunction(function); err != nil {
					// A function its registry refuses belongs to no
					// generation, so it goes with the update.
					teardown.FreeFunction(function)
					return fmt.Errorf("upsert function %q: %w", config.Name, err)
				}
			}
			return nil
		},
	})
}

// checkFunction refuses a function whose names its fields cannot hold
// whole or whose chain weighs past the bound, as C's function
// configuration refuses the weight.
func checkFunction(function FunctionConfig) error {
	err := checkNames("function", nameField{"name", function.Name, len(layout.CpFunction{}.Name)})
	if err != nil {
		return err
	}
	for _, weighted := range function.Chains {
		chain := weighted.Chain
		if weighted.Weight > maxWeightSum {
			return fmt.Errorf("function %q: chain %q weighs %d, past %d", function.Name, chain.Name, weighted.Weight, maxWeightSum)
		}
		if err := checkNames("chain", nameField{"name", chain.Name, len(layout.CpChain{}.Name)}); err != nil {
			return err
		}
		for _, module := range chain.Modules {
			err := checkNames(
				"chained module",
				nameField{"type", module.Type, len(layout.CpChainModule{}.Type)},
				nameField{"name", module.Name, len(layout.CpChainModule{}.Name)},
			)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// refuseRepeated refuses an update that names one function or pipeline
// twice.
func refuseRepeated(kind string, names []string) error {
	for idx, name := range names {
		if slices.Contains(names[:idx], name) {
			return fmt.Errorf("%s %q: %w", kind, name, ErrRepeated)
		}
	}
	return nil
}

// newFunction builds a function and its chains in the zone, as
// cp_function_new and cp_function_init do.
//
// A failed build frees what it built.
func (m *build) newFunction(config FunctionConfig) (*layout.CpFunction, error) {
	context := &m.cp.MemoryContext
	count := uint64(len(config.Chains))
	function := newNode[layout.CpFunction](context, unsafe.Sizeof(layout.CpFunctionChain{})*uintptr(count))
	if function == nil {
		return nil, fmt.Errorf("allocate function: %w", errZoneMemory)
	}
	function.MemoryContext.Set(context)
	function.ChainCount = count

	counterRegistryInit(&function.CounterRegistry, context)
	shm.CopyString(function.Name[:], config.Name)
	counters := []struct {
		Name string
		Size uint64
		ID   *uint64
	}{
		{"input", 2, &function.CounterPacketIn},
		{"output", 2, &function.CounterPacketOut},
		{"drop", 2, &function.CounterPacketDrop},
		{"input histogram", 8, &function.CounterPacketInHist},
		{"pending_input", 2, &function.CounterPacketPendingInput},
		{"pending_output", 2, &function.CounterPacketPendingOutput},
	}
	for _, counter := range counters {
		id, err := RegisterCounter(&function.CounterRegistry, counter.Name, counter.Size)
		if err != nil {
			teardown.FreeFunction(function)
			return nil, fmt.Errorf("register %q counter: %w", counter.Name, err)
		}
		*counter.ID = id
	}

	chains := function.Chains(int(count))
	for idx, weighted := range config.Chains {
		chain, err := newChain(context, weighted.Chain)
		if err != nil {
			teardown.FreeFunction(function)
			return nil, fmt.Errorf("chain %q: %w", weighted.Chain.Name, err)
		}
		chains[idx].CpChain.Set(chain)
		chains[idx].Weight = weighted.Weight
	}
	return function, nil
}

// newChain builds a chain in a function's memory, as cp_chain_new and
// cp_chain_init do.
//
// A failed build frees what it built.
func newChain(context *layout.MemoryContext, config ChainConfig) (*layout.CpChain, error) {
	length := uint64(len(config.Modules))
	chain := newNode[layout.CpChain](context, unsafe.Sizeof(layout.CpChainModule{})*uintptr(length))
	if chain == nil {
		return nil, fmt.Errorf("allocate chain: %w", errZoneMemory)
	}
	chain.MemoryContext.Set(context)
	chain.Length = length

	counterRegistryInit(&chain.CounterRegistry, context)
	shm.CopyString(chain.Name[:], config.Name)
	counters := []struct {
		Name string
		ID   *uint64
	}{
		{"pending_input", &chain.CounterPacketPendingInput},
		{"pending_output", &chain.CounterPacketPendingOutput},
	}
	for _, counter := range counters {
		id, err := RegisterCounter(&chain.CounterRegistry, counter.Name, 2)
		if err != nil {
			teardown.FreeChain(chain)
			return nil, fmt.Errorf("register %q counter: %w", counter.Name, err)
		}
		*counter.ID = id
	}

	modules := chain.Modules(int(length))
	for idx, module := range config.Modules {
		shm.CopyString(modules[idx].Type[:], module.Type)
		shm.CopyString(modules[idx].Name[:], module.Name)
	}
	return chain, nil
}

// upsertFunction puts a function into the generation's registry, as
// cp_function_registry_upsert does.
//
// It continues the counters of the function of its name, and each chain
// those of that function's chain of its name. A failed link writes only
// into the function's own registries, which go with the function.
func (m *build) upsertFunction(function *layout.CpFunction) error {
	name := shm.String(function.Name[:])
	old := registry.LookupFunction(&m.gen.FunctionRegistry, name)
	var oldCounters *layout.CounterRegistry
	if old != nil {
		oldCounters = &old.CounterRegistry
	}
	if err := linkCounters(&function.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}

	chains := function.Chains(int(function.ChainCount))
	for idx := range chains {
		chain := chains[idx].CpChain.Get()
		var oldChainCounters *layout.CounterRegistry
		if old != nil {
			oldChains := old.Chains(int(old.ChainCount))
			for oldIdx := range oldChains {
				oldChain := oldChains[oldIdx].CpChain.Get()
				if shm.Equal(oldChain.Name[:], shm.String(chain.Name[:])) {
					oldChainCounters = &oldChain.CounterRegistry
					break
				}
			}
		}
		if err := linkCounters(&chain.CounterRegistry, oldChainCounters); err != nil {
			return fmt.Errorf("link counter registry of chain %q: %w", shm.String(chain.Name[:]), err)
		}
	}

	key := registry.FieldKey(name, len(function.Name))
	if !registry.Replace(
		&m.gen.FunctionRegistry.Registry, functionHash, registry.HashName(key),
		registry.FunctionMatch(key), &function.ConfigItem, teardown.FreeFunctionItem,
	) {
		return fmt.Errorf("replace function in registry: %w", errZoneMemory)
	}
	return nil
}

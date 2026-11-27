//go:build linux && (amd64 || arm64)

package zone

import (
	"fmt"
	"iter"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Generation is a published configuration generation this process
// pinned: its registries and configurations stay valid until Release.
type Generation struct {
	Gen *layout.CpConfigGen
	// cp is the control-plane zone of the instance the generation was
	// pinned in.
	cp *layout.CpConfig
}

// Pin returns the generation the instance publishes now, with a reference
// held.
//
// The publication lock is held only while the generation is loaded,
// checked and referenced, as C's own readers hold it. The generation is
// the only shared address this reader writes through that attach did not
// check, since each publication may replace it, so one outside the
// instance is refused before the increment touches it; its alignment is
// not checked (attach: trust).
func (m *Instance) Pin() (*Generation, error) {
	shm.LockSpin(&m.Cp.GenLock.Locked)
	defer shm.UnlockSpin(&m.Cp.GenLock.Locked)
	gen := m.Cp.CpConfigGen.Load()
	if gen == nil {
		return &Generation{}, nil
	}
	if !contains(m.memory, unsafe.Pointer(gen), unsafe.Sizeof(*gen)) {
		return nil, fmt.Errorf("instance %d: published generation lies outside the instance", m.Index)
	}
	atomic.AddUint64(&gen.Refcnt, 1)
	return &Generation{Gen: gen, cp: m.Cp}, nil
}

// Release drops the reference Pin took, as cp_config_gen_release does; a
// second call does nothing.
//
// A publication that retired the generation while it was pinned left it
// to the last reference, so the release that drops it frees the
// generation and then reclaims the superseded agents only it held. It
// takes the agent lock, which the caller must not hold. A generation no
// pin produced is left alone.
func (m *Generation) Release() {
	gen := m.Gen
	m.Gen = nil
	if gen == nil || m.cp == nil {
		return
	}
	teardown.ReleaseGeneration(m.cp, gen)
}

// LookupModule returns the module configuration of a type and name the
// generation publishes, or nil, as cp_config_gen_lookup_module does.
//
// A generation that publishes nothing or was released finds nothing.
func (m *Generation) LookupModule(moduleType, name string) *layout.CpModule {
	if m.Gen == nil {
		return nil
	}
	return registry.LookupModule(&m.Gen.ModuleRegistry, moduleType, name)
}

// LookupObject returns the object of a type and name the generation
// publishes, or nil, as cp_config_gen_lookup_object does.
//
// A generation that publishes nothing or was released finds nothing.
func (m *Generation) LookupObject(objectType, name string) *layout.CpObject {
	if m.Gen == nil {
		return nil
	}
	return registry.LookupObject(&m.Gen.ObjectRegistry, objectType, name)
}

// ModulePosition is a place in the topology where a chain runs a module
// configuration.
//
// It names the device, the pipeline on its input or output, the function,
// the chain and the configuration's type and name.
type ModulePosition struct {
	Device     string
	Pipeline   string
	Function   string
	Chain      string
	ModuleType string
	ModuleName string
}

// ModulePositions yields every position of a module type in the
// generation's topology.
//
// It is the counterpart of the cgo agent's module position walk. The walk
// goes over devices in registry order, each device's input pipelines and
// then its output pipelines, their functions, chains and chain modules in
// order. A pipeline or function no registry holds is skipped. A
// generation that publishes nothing or was released yields nothing; the
// positions are read while iterating, so the pin must be held until the
// iteration ends.
func (m *Generation) ModulePositions(moduleType string) iter.Seq[ModulePosition] {
	return func(yield func(ModulePosition) bool) {
		if m.Gen == nil {
			return
		}
		for _, item := range registry.Items(&m.Gen.DeviceRegistry.EntityRegistry.Registry) {
			device := registry.Container[layout.CpDevice](item, 0)
			position := ModulePosition{Device: shm.String(device.Name[:])}
			for _, entry := range []*layout.CpDeviceEntry{device.InputPipelines.Get(), device.OutputPipelines.Get()} {
				pipelines := entry.Pipelines(int(entry.PipelineCount))
				for idx := range pipelines {
					position.Pipeline = shm.String(pipelines[idx].Name[:])
					if !m.pipelinePositions(position, moduleType, yield) {
						return
					}
				}
			}
		}
	}
}

// pipelinePositions yields the positions of a module type in the pipeline
// the position names, and reports whether the walk goes on.
func (m *Generation) pipelinePositions(position ModulePosition, moduleType string, yield func(ModulePosition) bool) bool {
	pipeline := registry.LookupPipeline(&m.Gen.PipelineRegistry, position.Pipeline)
	if pipeline == nil {
		return true
	}
	functions := pipeline.Functions(int(pipeline.Length))
	for idx := range functions {
		position.Function = shm.String(functions[idx].Name[:])
		function := registry.LookupFunction(&m.Gen.FunctionRegistry, position.Function)
		if function == nil {
			continue
		}
		chains := function.Chains(int(function.ChainCount))
		for chainIdx := range chains {
			chain := chains[chainIdx].CpChain.Get()
			position.Chain = shm.String(chain.Name[:])
			modules := chain.Modules(int(chain.Length))
			for moduleIdx := range modules {
				if !shm.Equal(modules[moduleIdx].Type[:], moduleType) {
					continue
				}
				position.ModuleType = moduleType
				position.ModuleName = shm.String(modules[moduleIdx].Name[:])
				if !yield(position) {
					return false
				}
			}
		}
	}
	return true
}

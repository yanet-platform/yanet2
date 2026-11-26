//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// PipelineConfig is a pipeline: its name and the functions it runs, in
// order.
type PipelineConfig struct {
	Name      string
	Functions []string
}

// UpdatePipelines publishes a generation in which the pipelines replace
// those of their names, as cp_config_update_pipelines does.
//
// It returns once every worker runs the generation, with no timeout, as C
// does. A name a field cannot hold whole is refused before the instance
// is touched; a pipeline named twice is refused before the copy, and one
// a device runs with a function the generation does not publish fails the
// contexts' build. Each pipeline continues the counters of the one it
// replaces. A refused update publishes nothing.
func UpdatePipelines(instance *zone.Instance, pipelines []PipelineConfig, options ...UpdateOption) error {
	names := make([]string, 0, len(pipelines))
	for _, pipeline := range pipelines {
		if err := checkPipeline(pipeline); err != nil {
			return err
		}
		names = append(names, pipeline.Name)
	}
	return publish(instance, options, change{
		Check: func(*layout.CpConfigGen) error {
			return refuseRepeated("pipeline", names)
		},
		Put: func(m *build) error {
			for _, config := range pipelines {
				pipeline, err := m.newPipeline(config)
				if err != nil {
					return fmt.Errorf("pipeline %q: %w", config.Name, err)
				}
				if err := m.upsertPipeline(pipeline); err != nil {
					return fmt.Errorf("upsert pipeline %q: %w", config.Name, err)
				}
			}
			return nil
		},
	})
}

// checkPipeline refuses a pipeline whose names its fields cannot hold
// whole.
func checkPipeline(pipeline PipelineConfig) error {
	err := checkNames("pipeline", nameField{"name", pipeline.Name, len(layout.CpPipeline{}.Name)})
	if err != nil {
		return err
	}
	for _, function := range pipeline.Functions {
		err := checkNames("pipeline function", nameField{"name", function, len(layout.CpPipelineFunction{}.Name)})
		if err != nil {
			return err
		}
	}
	return nil
}

// newPipeline builds a pipeline in the zone, as cp_pipeline_new and
// cp_pipeline_init do.
//
// A failed build frees what it built.
func (m *build) newPipeline(config PipelineConfig) (*layout.CpPipeline, error) {
	context := &m.cp.MemoryContext
	length := uint64(len(config.Functions))
	pipeline := newNode[layout.CpPipeline](context, unsafe.Sizeof(layout.CpPipelineFunction{})*uintptr(length))
	if pipeline == nil {
		return nil, fmt.Errorf("allocate pipeline: %w", errZoneMemory)
	}
	pipeline.MemoryContext.Set(context)
	pipeline.Length = length

	shm.CopyString(pipeline.Name[:], config.Name)
	counterRegistryInit(&pipeline.CounterRegistry, context)
	counters := []struct {
		Name string
		Size uint64
		ID   *uint64
	}{
		{"input", 2, &pipeline.CounterPacketIn},
		{"output", 2, &pipeline.CounterPacketOut},
		{"drop", 2, &pipeline.CounterPacketDrop},
		{"input histogram", 8, &pipeline.CounterPacketInHist},
		{"pending_input", 2, &pipeline.CounterPacketPendingInput},
		{"pending_output", 2, &pipeline.CounterPacketPendingOutput},
	}
	for _, counter := range counters {
		id, err := RegisterCounter(&pipeline.CounterRegistry, counter.Name, counter.Size)
		if err != nil {
			teardown.FreePipeline(pipeline)
			return nil, fmt.Errorf("register %q counter: %w", counter.Name, err)
		}
		*counter.ID = id
	}

	functions := pipeline.Functions(int(length))
	for idx, function := range config.Functions {
		shm.CopyString(functions[idx].Name[:], function)
	}
	return pipeline, nil
}

// upsertPipeline puts a pipeline into the generation's registry,
// continuing the counters of the pipeline of its name, as
// cp_pipeline_registry_upsert does; a pipeline the upsert fails for stays
// allocated, as C leaves it.
func (m *build) upsertPipeline(pipeline *layout.CpPipeline) error {
	name := shm.String(pipeline.Name[:])
	var oldCounters *layout.CounterRegistry
	if old := m.lookupPipeline(name); old != nil {
		oldCounters = &old.CounterRegistry
	}
	if err := linkCounters(&pipeline.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	key := registry.FieldKey(name, len(pipeline.Name))
	if !registry.Replace(
		&m.gen.PipelineRegistry.Registry, pipelineHash, registry.HashName(key),
		pipelineMatch(key), &pipeline.ConfigItem, teardown.FreePipelineItem,
	) {
		return fmt.Errorf("replace pipeline in registry: %w", errZoneMemory)
	}
	return nil
}

// pipelineMatch matches the pipeline of a name already cut to its field.
func pipelineMatch(key string) registry.Match {
	return func(item *layout.RegistryItem) bool {
		return shm.Equal(registry.Pipeline(item).Name[:], key)
	}
}

// lookupPipeline returns the pipeline of a name the generation holds, or
// nil.
func (m *build) lookupPipeline(name string) *layout.CpPipeline {
	pipelines := &m.gen.PipelineRegistry.Registry
	key := registry.FieldKey(name, len(layout.CpPipeline{}.Name))
	index, ok := registry.Lookup(pipelines, registry.HashName(key), pipelineMatch(key))
	if !ok {
		return nil
	}
	return registry.Pipeline(registry.Item(pipelines, index))
}

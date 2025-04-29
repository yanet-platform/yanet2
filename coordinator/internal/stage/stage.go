package stage

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"github.com/yanet-platform/yanet2/coordinator/internal/registry"
)

type options struct {
	Log *zap.SugaredLogger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop().Sugar(),
	}
}

// StageOption is a function that configures the Stage.
type StageOption func(*options)

// WithLog sets the logger for the Stage.
func WithLog(log *zap.SugaredLogger) StageOption {
	return func(o *options) {
		o.Log = log
	}
}

// Stage is a manager for the multi-stage configuration process.
//
// It is responsible for managing a single stage configuration.
type Stage struct {
	cfg      Config
	registry *registry.Registry
	pipeline ynpb.PipelineServiceClient
	log      *zap.SugaredLogger
}

// NewStage creates a new Stage.
func NewStage(
	cfg Config,
	registry *registry.Registry,
	pipeline ynpb.PipelineServiceClient,
	options ...StageOption,
) *Stage {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With("stage", cfg.Name)

	return &Stage{
		cfg:      cfg,
		registry: registry,
		pipeline: pipeline,
		log:      log,
	}
}

// Setup applies the stage configuration to the modules.
func (m *Stage) Setup(ctx context.Context) error {
	m.log.Infow("setting up stage")
	defer m.log.Infow("finished setting up stage")

	if err := m.setupNUMAConfigs(ctx); err != nil {
		return fmt.Errorf("failed to setup NUMA configs: %w", err)
	}

	for numaIdx, numaConfig := range m.cfg.NUMA {
		if err := m.setupPipelines(ctx, numaIdx, numaConfig.Pipelines); err != nil {
			return fmt.Errorf("failed to setup pipeline: %w", err)
		}
		if err := m.assignPipelines(ctx, numaIdx, numaConfig.Devices); err != nil {
			return fmt.Errorf("failed to assign pipeline to devices: %w", err)
		}
	}

	return nil
}

func (m *Stage) setupNUMAConfigs(ctx context.Context) error {
	wg, ctx := errgroup.WithContext(ctx)
	for numaIdx, numaConfig := range m.cfg.NUMA {
		wg.Go(func() error {
			return m.setupNUMAConfig(ctx, numaIdx, numaConfig)
		})
	}

	return wg.Wait()
}

// setupNUMAConfig applies a NUMA config to the modules.
func (m *Stage) setupNUMAConfig(ctx context.Context, numaIdx NUMAIdx, cfg NUMAConfig) error {
	m.log.Infow("setting up NUMA config",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("config", cfg),
	)
	defer m.log.Infow("finished setting up NUMA config",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("config", cfg),
	)

	if err := m.setupModulesConfigs(ctx, numaIdx, cfg.Modules); err != nil {
		return fmt.Errorf("failed to setup modules configs: %w", err)
	}

	return nil
}

func (m *Stage) setupModulesConfigs(ctx context.Context, numaIdx NUMAIdx, modules map[string]ModuleConfig) error {
	wg, ctx := errgroup.WithContext(ctx)
	for name, cfg := range modules {
		wg.Go(func() error {
			return m.setupModuleConfig(ctx, numaIdx, name, cfg)
		})
	}

	return wg.Wait()
}

func (m *Stage) setupModuleConfig(ctx context.Context, numaIdx NUMAIdx, name string, cfg ModuleConfig) error {
	m.log.Infow("setting up module config",
		zap.String("module", name),
	)
	defer m.log.Infow("finished setting up module config",
		zap.String("module", name),
	)

	mod, ok := m.registry.GetModule(name)
	if !ok {
		return fmt.Errorf("module %q not found", name)
	}

	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := mod.SetupConfig(ctx, uint32(numaIdx), cfg.ConfigName, data); err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	return nil
}

func (m *Stage) setupPipelines(ctx context.Context, numaIdx NUMAIdx, pipelines []PipelineConfig) error {
	m.log.Infow("setting up pipelines",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("pipelines", pipelines),
	)
	defer m.log.Infow("finished setting up pipelines",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("pipelines", pipelines),
	)

	req := &ynpb.UpdatePipelinesRequest{
		Numa: uint32(numaIdx),
	}

	for _, pipeline := range pipelines {
		nodes := make([]*ynpb.PipelineChainNode, 0, len(pipeline.Chain))
		for _, node := range pipeline.Chain {
			nodes = append(nodes, &ynpb.PipelineChainNode{
				ModuleName: node.ModuleName,
				ConfigName: node.ConfigName,
			})
		}

		req.Chains = append(req.Chains, &ynpb.PipelineChain{
			Name:  pipeline.Name,
			Nodes: nodes,
		})
	}

	if _, err := m.pipeline.Update(ctx, req); err != nil {
		return fmt.Errorf("failed to update pipeline: %w", err)
	}

	return nil
}

func (m *Stage) assignPipelines(ctx context.Context, numaIdx NUMAIdx, devices []DeviceConfig) error {
	m.log.Infow("assigning pipelines to devices",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("devices", devices),
	)
	defer m.log.Infow("finished assigning pipelines to devices",
		zap.Uint32("numa", uint32(numaIdx)),
		zap.Any("devices", devices),
	)

	req := &ynpb.AssignPipelinesRequest{
		Numa:    uint32(numaIdx),
		Devices: map[uint32]*ynpb.DevicePipelines{},
	}

	for _, device := range devices {
		devicePipelines := make([]*ynpb.DevicePipeline, 0, len(device.Pipelines))
		for _, pipeline := range device.Pipelines {
			devicePipelines = append(devicePipelines, &ynpb.DevicePipeline{
				PipelineName:   pipeline.Name,
				PipelineWeight: pipeline.Weight,
			})
		}

		req.Devices[uint32(device.ID)] = &ynpb.DevicePipelines{
			Pipelines: devicePipelines,
		}
	}

	if _, err := m.pipeline.Assign(ctx, req); err != nil {
		return fmt.Errorf("failed to assign pipelines to devices: %w", err)
	}

	return nil
}

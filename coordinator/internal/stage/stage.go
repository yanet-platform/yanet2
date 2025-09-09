package stage

import (
	"context"
	"fmt"
	"strconv"

	"go.uber.org/zap"

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

	for instance, instanceConfig := range m.cfg.Instances {
		if err := m.setupPipelines(ctx, instance, instanceConfig.Pipelines); err != nil {
			return fmt.Errorf("failed to setup pipeline: %w", err)
		}
		if err := m.assignPipelines(ctx, instance, instanceConfig.Devices); err != nil {
			return fmt.Errorf("failed to assign pipeline to devices: %w", err)
		}
	}

	return nil
}

func (m *Stage) setupPipelines(ctx context.Context, instance DataplaneInstanceIdx, pipelines []PipelineConfig) error {
	m.log.Infow("setting up pipelines",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("pipelines", pipelines),
	)
	defer m.log.Infow("finished setting up pipelines",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("pipelines", pipelines),
	)

	req := &ynpb.UpdatePipelinesRequest{
		Instance: uint32(instance),
	}

	for _, pipeline := range pipelines {
		req.Pipelines = append(req.Pipelines, &ynpb.Pipeline{
			Name: pipeline.Name,
			Functions: pipeline.Functions,
		})
	}

	if _, err := m.pipeline.Update(ctx, req); err != nil {
		return fmt.Errorf("failed to update pipeline: %w", err)
	}

	return nil
}

func (m *Stage) assignPipelines(ctx context.Context, instance DataplaneInstanceIdx, devices []DeviceConfig) error {
	m.log.Infow("assigning pipelines to devices",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("devices", devices),
	)
	defer m.log.Infow("finished assigning pipelines to devices",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("devices", devices),
	)

	req := &ynpb.AssignPipelinesRequest{
		Instance: uint32(instance),
		Devices:  map[string]*ynpb.DevicePipelines{},
	}

	for _, device := range devices {
		devicePipelines := make([]*ynpb.DevicePipeline, 0, len(device.Pipelines))
		for _, pipeline := range device.Pipelines {
			devicePipelines = append(devicePipelines, &ynpb.DevicePipeline{
				PipelineName:   pipeline.Name,
				PipelineWeight: pipeline.Weight,
			})
		}

		req.Devices[strconv.Itoa(int(device.ID))] = &ynpb.DevicePipelines{
			Pipelines: devicePipelines,
		}
	}

	if _, err := m.pipeline.Assign(ctx, req); err != nil {
		return fmt.Errorf("failed to assign pipelines to devices: %w", err)
	}

	return nil
}

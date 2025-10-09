package stage

import (
	"context"
	"fmt"

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
	device   ynpb.DeviceServiceClient
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
		if err := m.setupDevices(ctx, instance, instanceConfig.Devices); err != nil {
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
			Name:      pipeline.Name,
			Functions: pipeline.Functions,
		})
	}

	if _, err := m.pipeline.Update(ctx, req); err != nil {
		return fmt.Errorf("failed to update pipeline: %w", err)
	}

	return nil
}

func (m *Stage) setupDevices(ctx context.Context, instance DataplaneInstanceIdx, devices []DeviceConfig) error {
	m.log.Infow("setting up devices",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("devices", devices),
	)
	defer m.log.Infow("finished setting up devices",
		zap.Uint32("instance", uint32(instance)),
		zap.Any("devices", devices),
	)

	req := &ynpb.UpdateDevicesRequest{
		Instance: uint32(instance),
	}

	for _, device := range devices {
		reqDevice := &ynpb.Device{
			Name: device.Name,
		}
		for _, pipeline := range device.Input {
			reqDevice.Input = append(reqDevice.Input, &ynpb.DevicePipeline{
				Name:   pipeline.Name,
				Weight: pipeline.Weight,
			})
		}
		for _, pipeline := range device.Output {
			reqDevice.Output = append(reqDevice.Output, &ynpb.DevicePipeline{
				Name:   pipeline.Name,
				Weight: pipeline.Weight,
			})
		}

		req.Devices = append(req.Devices, reqDevice)
	}

	if _, err := m.device.Update(ctx, req); err != nil {
		return fmt.Errorf("failed to set up devices: %w", err)
	}

	return nil
}

package operator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb"
)

// GatewayActuator applies a StageConfig to a single Gateway and prunes
// pipelines that are no longer part of the desired stage.
type GatewayActuator struct {
	name      string
	conn      *grpc.ClientConn
	pipelines ynpb.PipelineServiceClient
	plain     plainpb.DevicePlainServiceClient
	vlan      vlanpb.DeviceVlanServiceClient

	metrics GatewayActuatorMetricsObserver
	log     *zap.Logger
}

// NewGatewayActuator dials the Gateway endpoint and returns a ready-to-use
// actuator.
func NewGatewayActuator(
	cfg operator.GatewayConfig,
	options ...GatewayActuatorOption,
) (*GatewayActuator, error) {
	opts := newGatewayActuatorOptions()
	for _, o := range options {
		o(opts)
	}

	endpoint := cfg.Endpoint.Unwrap()
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	return &GatewayActuator{
		name:      cfg.Name,
		conn:      conn,
		pipelines: ynpb.NewPipelineServiceClient(conn),
		plain:     plainpb.NewDevicePlainServiceClient(conn),
		vlan:      vlanpb.NewDeviceVlanServiceClient(conn),
		metrics:   opts.Metrics,
		log:       opts.Log.With(zap.String("gateway", cfg.Name)),
	}, nil
}

func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}

// Apply pushes the stage to the Gateway and best-effort prunes stale
// pipelines.
//
// Garbage collection failures are logged as warnings and never propagate.
func (m *GatewayActuator) Apply(ctx context.Context, stage *StageConfig) error {
	err := m.applyStage(ctx, stage)
	m.metrics.OnApplyCompleted(err)
	if err != nil {
		return fmt.Errorf("failed to apply stage to gateway %q: %w", m.name, err)
	}

	m.log.Info("applied stage",
		zap.String("stage", stage.Name),
	)

	if err := m.gc(ctx, stage); err != nil {
		m.log.Warn("garbage collection failed", zap.Error(err))
	}

	return nil
}

// applyStage pushes pipelines and device bindings to the Gateway.
//
// Pipelines are updated first so subsequent device bindings can reference
// them.
func (m *GatewayActuator) applyStage(ctx context.Context, stage *StageConfig) error {
	for _, p := range stage.Pipelines {
		_, err := m.pipelines.Update(
			ctx,
			&ynpb.UpdatePipelineRequest{
				Pipeline: pipelineToProto(p),
			},
		)
		m.metrics.OnResourceUpdated(kindPipeline, err)
		if err != nil {
			return fmt.Errorf("update pipeline %q: %w", p.Name, err)
		}

		m.log.Debug("applied pipeline",
			zap.String("pipeline", p.Name),
			zap.Strings("functions", p.Functions),
		)
	}

	for _, d := range stage.Devices.Plain {
		_, err := m.plain.UpdateDevice(ctx, plainDeviceToProto(d))
		m.metrics.OnResourceUpdated(kindDevicePlain, err)
		if err != nil {
			return fmt.Errorf("update plain device %q: %w", d.Name, err)
		}

		m.log.Debug("applied plain device",
			zap.String("device", d.Name),
			zap.Strings("input", devicePipelineRefStrings(d.Input)),
			zap.Strings("output", devicePipelineRefStrings(d.Output)),
		)
	}

	for _, d := range stage.Devices.VLAN {
		_, err := m.vlan.UpdateDevice(ctx, vlanDeviceToProto(d))
		m.metrics.OnResourceUpdated(kindDeviceVlan, err)
		if err != nil {
			return fmt.Errorf("update vlan device %q: %w", d.Name, err)
		}

		m.log.Debug("applied vlan device",
			zap.String("device", d.Name),
			zap.Uint32("vlan", d.VLAN),
			zap.Strings("input", devicePipelineRefStrings(d.Input)),
			zap.Strings("output", devicePipelineRefStrings(d.Output)),
		)
	}

	return nil
}

// gc deletes pipelines that exist on the Gateway but are not
// part of the desired stage.
//
// Per-pipeline Delete failures are logged and skipped: pipelines still
// referenced by a device will fail (PipelineService.Delete refuses while
// any device references the pipeline), and that is expected behavior the
// caller should not treat as fatal.
//
// Functions are intentionally not GC'd here: they are owned by their own
// per-function operators (route-operator, acl-operator, ...).
func (m *GatewayActuator) gc(ctx context.Context, stage *StageConfig) error {
	desired := make(map[string]struct{}, len(stage.Pipelines))
	for _, p := range stage.Pipelines {
		desired[p.Name] = struct{}{}
	}

	list, err := m.pipelines.List(ctx, &ynpb.ListPipelinesRequest{})
	if err != nil {
		m.metrics.OnGC(0, 0, err)
		return fmt.Errorf("list pipelines: %w", err)
	}

	deleted, failed := 0, 0
	for _, id := range list.GetIds() {
		if _, ok := desired[id.GetName()]; ok {
			continue
		}

		req := &ynpb.DeletePipelineRequest{Id: id}
		if _, err := m.pipelines.Delete(ctx, req); err != nil {
			failed++
			m.log.Warn("failed to delete stale pipeline",
				zap.String("pipeline", id.GetName()),
				zap.Error(err),
			)
			continue
		}

		deleted++
		m.log.Info("deleted stale pipeline",
			zap.String("pipeline", id.GetName()),
		)
	}

	m.metrics.OnGC(deleted, failed, nil)
	return nil
}

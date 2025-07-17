package forward

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

type ffiConfigUpdater func(m *ForwardService, name string, instance uint32) error

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu          sync.Mutex
	agents      []*ffi.Agent
	log         *zap.SugaredLogger
	configs     map[instanceKey][]ForwardDeviceConfig
	deviceCount uint16
	updater     ffiConfigUpdater
}

func NewForwardService(agents []*ffi.Agent, log *zap.SugaredLogger, deviceCount uint16) *ForwardService {
	return &ForwardService{
		agents:      agents,
		log:         log,
		configs:     make(map[instanceKey][]ForwardDeviceConfig),
		deviceCount: deviceCount,
		updater:     updateModuleConfig,
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {

	response := &forwardpb.ListConfigsResponse{
		InstanceConfigs: make([]*forwardpb.InstanceConfigs, len(m.agents)),
	}
	for inst := range m.agents {
		response.InstanceConfigs[inst] = &forwardpb.InstanceConfigs{
			Instance: uint32(inst),
		}
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range m.configs {
		instConfig := response.InstanceConfigs[key.dataplaneInstance]
		instConfig.Configs = append(instConfig.Configs, key.name)
	}

	return response, nil
}

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	key := instanceKey{name: name, dataplaneInstance: inst}
	response := &forwardpb.ShowConfigResponse{Instance: inst}

	m.mu.Lock()
	defer m.mu.Unlock()

	config := m.configs[key]
	if config != nil {
		devices := make([]*forwardpb.ForwardDeviceConfig, 0, m.deviceCount)
		for srcDevIdx, device := range config {
			deviceConfig := &forwardpb.ForwardDeviceConfig{
				SrcDevId: uint32(srcDevIdx),
				DstDevId: uint32(device.DstDevId),
				Forwards: make([]*forwardpb.L3ForwardEntry, 0, len(device.Forwards)),
			}

			for network, targetDevice := range device.Forwards {
				deviceConfig.Forwards = append(deviceConfig.Forwards, &forwardpb.L3ForwardEntry{
					Network:  network.String(),
					DstDevId: uint32(targetDevice),
				})
			}
			slices.SortFunc(deviceConfig.Forwards, func(a, b *forwardpb.L3ForwardEntry) int {
				if devIDCmp := cmp.Compare(a.DstDevId, b.DstDevId); devIDCmp != 0 {
					return devIDCmp
				}
				return cmp.Compare(a.Network, b.Network)
			})

			devices = append(devices, deviceConfig)
		}
		slices.SortFunc(devices, func(a, b *forwardpb.ForwardDeviceConfig) int {
			if devIDCmp := cmp.Compare(a.SrcDevId, b.SrcDevId); devIDCmp != 0 {
				return devIDCmp
			}
			return cmp.Compare(a.DstDevId, b.DstDevId)
		})

		response.Config = &forwardpb.Config{Devices: devices}
	}

	return response, nil
}

func (m *ForwardService) EnableL2Forward(ctx context.Context, req *forwardpb.L2ForwardEnableRequest) (*forwardpb.L2ForwardEnableResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	srcDevId, _, dstDevId, err := m.validateForwardParams(req.SrcDevId, "::/0", req.DstDevId)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Enable (or override) forwarding between devices
	key := instanceKey{name: name, dataplaneInstance: inst}
	config, exists := m.configs[key]
	if !exists {
		config = defaultForwardConfig(m.deviceCount)
		m.configs[key] = config
	}

	// Update in-memory configuration
	config[srcDevId].DstDevId = dstDevId

	// FIXME: Commit in-memory config only if SHM updates are successful?

	// Then update shm configs
	if err := m.updater(m, name, inst); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully enable l2 forward",
		zap.String("name", name),
		zap.Uint16("src_dev_id", uint16(srcDevId)),
		zap.Uint16("dst_dev_id", uint16(dstDevId)),
		zap.Uint32("instance", inst),
	)

	return &forwardpb.L2ForwardEnableResponse{}, nil
}

func (m *ForwardService) AddL3Forward(ctx context.Context, req *forwardpb.AddL3ForwardRequest) (*forwardpb.AddL3ForwardResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	if req.Forward == nil {
		return nil, status.Errorf(codes.InvalidArgument, "forward entry cannot be nil")
	}
	srcDevId, network, dstDevId, err := m.validateForwardParams(req.SrcDevId, req.Forward.Network, req.Forward.DstDevId)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// First update in-memory configs
	key := instanceKey{name: name, dataplaneInstance: inst}
	config := m.configs[key]
	if config == nil {
		config = defaultForwardConfig(m.deviceCount)
		m.configs[key] = config
	}

	sourceDev := &config[srcDevId]
	if sourceDev.Forwards == nil {
		sourceDev.Forwards = make(map[netip.Prefix]DeviceID)
	}
	sourceDev.Forwards[network] = dstDevId

	// FIXME: Commit in-memory config only if SHM updates are successful?

	// Then update shm configs
	if err := m.updater(m, name, inst); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully added forward",
		zap.String("name", name),
		zap.Uint32("src_dev_id", uint32(srcDevId)),
		zap.String("network", network.String()),
		zap.Uint32("dst_dev_id", uint32(dstDevId)),
	)

	return &forwardpb.AddL3ForwardResponse{}, nil
}

func (m *ForwardService) RemoveL3Forward(ctx context.Context, req *forwardpb.RemoveL3ForwardRequest) (*forwardpb.RemoveL3ForwardResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	srcDevId, network, _, err := m.validateForwardParams(req.SrcDevId, req.Network, 0)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// First update in-memory configs
	key := instanceKey{name: name, dataplaneInstance: inst}
	config, exists := m.configs[key]
	if !exists {
		return &forwardpb.RemoveL3ForwardResponse{}, nil
	}

	sourceDev := &config[srcDevId]
	if sourceDev.Forwards == nil {
		return &forwardpb.RemoveL3ForwardResponse{}, nil
	}
	delete(sourceDev.Forwards, network)

	// FIXME: Commit in-memory config only if SHM updates are successful?

	// Then update shm configs
	if err := m.updater(m, name, inst); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully removed forward",
		zap.String("name", name),
		zap.Uint16("src_dev_id", uint16(srcDevId)),
		zap.String("network", network.String()),
	)

	return &forwardpb.RemoveL3ForwardResponse{}, nil
}

func (m *ForwardService) DeleteConfig(ctx context.Context, req *forwardpb.DeleteConfigRequest) (*forwardpb.DeleteConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	// Remove module configuration from the control plane.
	delete(m.configs, instanceKey{name, inst})

	deleted := DeleteConfig(m, name, inst)
	m.log.Infow("deleted module config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
		zap.Bool("dataplane_hit", deleted),
	)

	response := &forwardpb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}

func (m *ForwardService) validateForwardParams(srcDeviceId uint32, network string, dstDeviceId uint32) (DeviceID, netip.Prefix, DeviceID, error) {
	prefix, err := netip.ParsePrefix(network)
	if err != nil {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "failed to parse network: %v", err)
	}

	sourceDeviceId := DeviceID(srcDeviceId)
	if uint32(sourceDeviceId) != srcDeviceId {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "source device ID is too large for uint16")
	}

	if uint16(sourceDeviceId) >= m.deviceCount {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "source device ID %d is out of range [0..%d)", sourceDeviceId, m.deviceCount)
	}

	targetDeviceId := DeviceID(dstDeviceId)
	if uint32(targetDeviceId) != dstDeviceId {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "destination device ID is too large for uint16")
	}
	if uint16(targetDeviceId) >= m.deviceCount {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "destination device ID %d is out of range [0..%d)", targetDeviceId, m.deviceCount)
	}

	return sourceDeviceId, prefix, targetDeviceId, nil
}

func updateModuleConfig(
	m *ForwardService,
	name string,
	instance uint32,
) error {
	m.log.Debugw("update config", zap.String("config", name), zap.Uint32("instance", instance))

	agent := m.agents[instance]
	if agent == nil {
		return fmt.Errorf("agent for instance %d is nil", instance)
	}

	key := instanceKey{name: name, dataplaneInstance: instance}
	config := m.configs[key]
	if config == nil {
		config = defaultForwardConfig(m.deviceCount)
		m.configs[key] = config
	}

	moduleConfig, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	// Configure all forwards
	for idx, device := range config {
		srcDeviceID := DeviceID(idx)
		dstDeviceID := device.DstDevId

		if err := moduleConfig.L2ForwardEnable(srcDeviceID, dstDeviceID); err != nil {
			return fmt.Errorf("failed to enable forward from dev(%d) to dev(%d) on instance %d: %w", srcDeviceID, dstDeviceID, instance, err)
		}

		// Then configure all forwards for this device
		for network, targetDevice := range device.Forwards {
			if err := moduleConfig.L3ForwardEnable(network, srcDeviceID, targetDevice); err != nil {
				return fmt.Errorf("failed to enable forward from dev(%d) to dev(%d) for network %s on instance %d: %w",
					srcDeviceID, targetDevice, network, instance, err)
			}
		}
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	m.log.Infow("successfully updated module",
		zap.String("name", name),
		zap.Uint32("instance", instance),
	)
	return nil
}

func defaultForwardConfig(deviceCount uint16) []ForwardDeviceConfig {
	config := make([]ForwardDeviceConfig, deviceCount)
	for idx := range config {
		config[idx].DstDevId = DeviceID(idx)
	}
	return config
}

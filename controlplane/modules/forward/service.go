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
	"github.com/yanet-platform/yanet2/controlplane/modules/forward/forwardpb"
)

type ffiConfigUpdater func(m *ForwardService, name string, numaIndices []uint32) error

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	log     *zap.SugaredLogger
	configs map[instanceKey]*ForwardConfig // instance key -> config
	updater ffiConfigUpdater
}

func NewForwardService(agents []*ffi.Agent, log *zap.SugaredLogger) *ForwardService {
	return &ForwardService{
		agents:  agents,
		log:     log,
		configs: make(map[instanceKey]*ForwardConfig),
		updater: updateModuleConfigs,
	}
}

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	name, numaIndices, err := validateTarget(req.Target, len(m.agents))
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	configs := make([]*forwardpb.InstanceConfig, 0, len(numaIndices))
	for _, numaIdx := range numaIndices {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config := m.configs[key]
		if config == nil {
			config = &ForwardConfig{}
		}

		devices := make([]*forwardpb.ForwardDeviceConfig, 0, len(config.DeviceForwards))
		for _, device := range config.DeviceForwards {
			deviceConfig := &forwardpb.ForwardDeviceConfig{
				DeviceId: uint32(device.L2ForwardDeviceID),
				Forwards: make([]*forwardpb.ForwardEntry, 0, len(device.Forwards)),
			}

			for network, targetDevice := range device.Forwards {
				deviceConfig.Forwards = append(deviceConfig.Forwards, &forwardpb.ForwardEntry{
					Network:  network.String(),
					DeviceId: uint32(targetDevice),
				})
			}
			slices.SortFunc(deviceConfig.Forwards, func(a, b *forwardpb.ForwardEntry) int {
				if devIDCmp := cmp.Compare(a.DeviceId, b.DeviceId); devIDCmp != 0 {
					return devIDCmp
				}
				return cmp.Compare(a.Network, b.Network)
			})

			devices = append(devices, deviceConfig)
		}

		configs = append(configs, &forwardpb.InstanceConfig{
			Numa:    numaIdx,
			Devices: devices,
		})
	}

	return &forwardpb.ShowConfigResponse{
		Configs: configs,
	}, nil
}

func (m *ForwardService) AddDevice(ctx context.Context, req *forwardpb.AddDeviceRequest) (*forwardpb.AddDeviceResponse, error) {
	name, numa, err := validateTarget(req.Target, len(m.agents))
	if err != nil {
		return nil, err
	}

	devId := req.DeviceId

	m.mu.Lock()
	defer m.mu.Unlock()

	// First check if device already exists in any configuration
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config, exists := m.configs[key]
		if !exists {
			continue
		}

		// Check if device already exists
		for _, device := range config.DeviceForwards {
			if device.L2ForwardDeviceID == ForwardDeviceID(devId) {
				return nil, status.Errorf(codes.AlreadyExists,
					"device with ID %d already exists on NUMA node %d", devId, numaIdx)
			}
		}
	}

	// Then update in-memory configs
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config, exists := m.configs[key]
		if !exists {
			config = &ForwardConfig{}
			m.configs[key] = config
		}

		// Add new device
		config.DeviceForwards = append(config.DeviceForwards, ForwardDeviceConfig{
			L2ForwardDeviceID: ForwardDeviceID(devId),
			Forwards:          make(map[netip.Prefix]ForwardDeviceID),
		})
	}

	// Then update shm configs
	if err := m.updater(m, name, numa); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully added device",
		zap.String("name", name),
		zap.Uint32("device", devId),
		zap.Uint32s("numa", numa),
	)

	return &forwardpb.AddDeviceResponse{}, nil
}

func (m *ForwardService) RemoveDevice(ctx context.Context, req *forwardpb.RemoveDeviceRequest) (*forwardpb.RemoveDeviceResponse, error) {
	name, numa, err := validateTarget(req.Target, len(m.agents))
	if err != nil {
		return nil, err
	}

	devId := req.DeviceId

	m.mu.Lock()
	defer m.mu.Unlock()

	// First check if the device exists in any config
	deviceFound := false

	ownDeviceForwardsCount := 0
	// Update in-memory configs
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config := m.configs[key]
		if config == nil {
			continue
		}

		// Find and remove device
		for i, device := range config.DeviceForwards {
			if device.L2ForwardDeviceID == ForwardDeviceID(devId) {
				deviceFound = true
				ownDeviceForwardsCount = len(device.Forwards)
				// Remove the device
				config.DeviceForwards = slices.Delete(config.DeviceForwards, i, i+1)
				break
			}
		}
	}

	removedForwards := make(map[netip.Prefix]bool)
	if deviceFound {
		// Remove any forwards that target the removed device
		for _, numaIdx := range numa {
			key := instanceKey{name: name, numaIdx: numaIdx}
			config := m.configs[key]
			if config == nil {
				continue
			}

			// Iterate through all devices and remove forwards targeting the removed device
			for i := range config.DeviceForwards {
				device := &config.DeviceForwards[i]
				// Create a new map to hold forwards that don't target the device we're removing
				newForwards := make(map[netip.Prefix]ForwardDeviceID, len(device.Forwards))

				// Copy only forwards that don't point to the removed device
				for prefix, targetID := range device.Forwards {
					if targetID != ForwardDeviceID(devId) {
						newForwards[prefix] = targetID
					} else {
						removedForwards[prefix] = true
					}
				}

				device.Forwards = newForwards
			}
		}
	} else {
		m.log.Warnw("device not found",
			zap.String("name", name),
			zap.Uint32("device", devId),
			zap.Uint32s("numa", numa),
		)
		return &forwardpb.RemoveDeviceResponse{}, nil
	}

	// Then update shm configs
	if err := m.updater(m, name, numa); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully removed device",
		zap.String("name", name),
		zap.Uint32("device", devId),
		zap.Int("own_forwards", ownDeviceForwardsCount),
		zap.Int("dangling_forwards", len(removedForwards)),
		zap.Uint32s("numa", numa),
	)

	return &forwardpb.RemoveDeviceResponse{}, nil
}

func (m *ForwardService) AddForward(ctx context.Context, req *forwardpb.AddForwardRequest) (*forwardpb.AddForwardResponse, error) {
	name, numa, err := validateTarget(req.Target, len(m.agents))
	if err != nil {
		return nil, err
	}
	if req.Forward == nil {
		return nil, status.Errorf(codes.InvalidArgument, "forward entry cannot be nil")
	}
	sourceDeviceId, network, targetDeviceId, err := validateForwardParams(req.DeviceId, req.Forward.Network, req.Forward.DeviceId)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// First update in-memory configs
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config := m.configs[key]
		if config == nil {
			config = &ForwardConfig{}
			m.configs[key] = config
		}

		// Check if the target device exists
		targetDeviceFound := false
		for i := range config.DeviceForwards {
			if config.DeviceForwards[i].L2ForwardDeviceID == targetDeviceId {
				targetDeviceFound = true
				break
			}
		}

		if !targetDeviceFound {
			return nil, status.Errorf(codes.NotFound, "target device with ID %d not found in NUMA node %d", targetDeviceId, numaIdx)
		}

		// Find the source device.
		sourceDeviceFound := false
		for i := range config.DeviceForwards {
			if config.DeviceForwards[i].L2ForwardDeviceID == sourceDeviceId {
				device := &config.DeviceForwards[i]
				if device.Forwards == nil {
					device.Forwards = make(map[netip.Prefix]ForwardDeviceID)
				}
				// Add or update forward entry
				device.Forwards[network] = targetDeviceId
				sourceDeviceFound = true
				break
			}
		}
		if !sourceDeviceFound {
			return nil, status.Errorf(codes.NotFound, "source device with ID %d not found in NUMA node %d", sourceDeviceId, numaIdx)
		}
	}

	// Then update shm configs
	if err := m.updater(m, name, numa); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully added forward",
		zap.String("name", name),
		zap.Uint32("source_id", uint32(sourceDeviceId)),
		zap.String("network", network.String()),
		zap.Uint32("target_id", uint32(targetDeviceId)),
	)

	return &forwardpb.AddForwardResponse{}, nil
}

func (m *ForwardService) RemoveForward(ctx context.Context, req *forwardpb.RemoveForwardRequest) (*forwardpb.RemoveForwardResponse, error) {
	name, numa, err := validateTarget(req.Target, len(m.agents))
	if err != nil {
		return nil, err
	}

	deviceId, network, _, err := validateForwardParams(req.DeviceId, req.Network, 0)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	forwardFound := false
	// First update in-memory configs
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config := m.configs[key]
		if config == nil {
			continue
		}

		// Find device and remove forward entry
		for i := range config.DeviceForwards {
			if config.DeviceForwards[i].L2ForwardDeviceID == deviceId {
				forwardFound = true
				delete(config.DeviceForwards[i].Forwards, network)
				break
			}
		}
	}

	if !forwardFound {
		m.log.Warnw("forward rule not found",
			zap.String("name", name),
			zap.Uint32("device", uint32(deviceId)),
			zap.String("network", network.String()),
		)
		return &forwardpb.RemoveForwardResponse{}, nil
	}

	// Then update shm configs
	if err := m.updater(m, name, numa); err != nil {
		return nil, fmt.Errorf("failed to update module configs: %w", err)
	}

	m.log.Infow("successfully removed forward",
		zap.String("name", name),
		zap.Uint32("device", uint32(deviceId)),
		zap.String("network", network.String()),
	)

	return &forwardpb.RemoveForwardResponse{}, nil
}

func updateModuleConfigs(
	m *ForwardService,
	name string,
	numaIndices []uint32,
) error {
	m.log.Debugw("updating configuration",
		zap.String("module", name),
		zap.Uint32s("numa", numaIndices),
	)

	// Create module configs for each NUMA node
	configs := make([]*ModuleConfig, len(numaIndices))
	for i, numaIdx := range numaIndices {

		agent := m.agents[numaIdx]
		if agent == nil {
			return fmt.Errorf("agent for NUMA %d is nil", numaIdx)
		}

		key := instanceKey{name: name, numaIdx: numaIdx}
		config := m.configs[key]
		if config == nil {
			config = &ForwardConfig{}
			m.configs[key] = config
		}

		// Count total number of devices for initialization
		deviceCount := uint16(len(config.DeviceForwards))
		if len(config.DeviceForwards) != int(deviceCount) {
			return fmt.Errorf("too many devices: %d", len(config.DeviceForwards))
		}

		moduleConfig, err := NewModuleConfig(agent, name, deviceCount)
		if err != nil {
			return fmt.Errorf("failed to create module config for NUMA %d: %w", numaIdx, err)
		}

		// Configure all forwards
		for idx, device := range config.DeviceForwards {
			srcDeviceID := ForwardDeviceID(idx)
			dstDeviceID := device.L2ForwardDeviceID

			// First enable the device itself
			if err := moduleConfig.DeviceEnable(srcDeviceID, dstDeviceID); err != nil {
				return fmt.Errorf("failed to enable device %d on NUMA %d: %w", dstDeviceID, numaIdx, err)
			}

			// Then configure all forwards for this device
			for network, targetDevice := range device.Forwards {
				if err := moduleConfig.ForwardEnable(network, srcDeviceID, targetDevice); err != nil {
					return fmt.Errorf("failed to enable forward from device %d to %d for network %s on NUMA %d: %w",
						dstDeviceID, targetDevice, network, numaIdx, err)
				}
			}
		}

		configs[i] = moduleConfig
	}

	// Apply all configurations
	for i, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]
		config := configs[i]

		if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
			return fmt.Errorf("failed to update module on NUMA %d: %w", numaIdx, err)
		}

		m.log.Debugw("successfully updated module config",
			zap.String("name", name),
			zap.Uint32("numa", numaIdx),
			zap.Int("device_count", len(m.configs[instanceKey{name: name, numaIdx: numaIdx}].DeviceForwards)),
		)
	}

	m.log.Infow("successfully updated all module configurations",
		zap.String("name", name),
		zap.Uint32s("numa", numaIndices),
	)

	return nil
}

func getNUMAIndices(requestedNuma []uint32, numAgents int) ([]uint32, error) {
	numaIndices := slices.Compact(slices.Sorted(slices.Values(requestedNuma)))

	slices.Sort(requestedNuma)
	if !slices.Equal(numaIndices, requestedNuma) {
		return nil, status.Error(codes.InvalidArgument, "duplicate NUMA indices in the request")
	}
	if len(numaIndices) > 0 && int(numaIndices[len(numaIndices)-1]) >= numAgents {
		return nil, status.Error(codes.InvalidArgument, "NUMA indices are out of range")
	}
	if len(numaIndices) == 0 {
		for idx := range numAgents {
			numaIndices = append(numaIndices, uint32(idx))
		}
	}
	return numaIndices, nil
}

func validateTarget(target *forwardpb.TargetModule, numAgents int) (string, []uint32, error) {
	if target == nil {
		return "", nil, status.Errorf(codes.InvalidArgument, "target cannot be nil")
	}

	name := target.ModuleName
	if name == "" {
		return "", nil, status.Errorf(codes.InvalidArgument, "module name is required")
	}
	numa, err := getNUMAIndices(target.Numa, numAgents)
	if err != nil {
		return "", nil, err
	}
	return name, numa, nil
}

func validateForwardParams(srcDeviceId uint32, network string, dstDeviceId uint32) (ForwardDeviceID, netip.Prefix, ForwardDeviceID, error) {
	prefix, err := netip.ParsePrefix(network)
	if err != nil {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "failed to parse network: %v", err)
	}

	sourceDeviceId := ForwardDeviceID(srcDeviceId)
	if uint32(sourceDeviceId) != srcDeviceId {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "source device ID is too large for uint16")
	}
	targetDeviceId := ForwardDeviceID(dstDeviceId)
	if uint32(targetDeviceId) != dstDeviceId {
		return 0, netip.Prefix{}, 0, status.Errorf(codes.InvalidArgument, "destination device ID is too large for uint16")
	}
	return sourceDeviceId, prefix, targetDeviceId, nil
}

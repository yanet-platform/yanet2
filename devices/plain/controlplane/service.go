package plain

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	mu sync.Mutex
	// deferred holds superseded devices whose free was refused because
	// a live configuration generation still referenced them. This
	// service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []DeviceConfig
	agent    *ffi.Agent
	configs  map[string]DeviceConfig
}

func NewDevicePlainService(agent *ffi.Agent) *DevicePlainService {
	return &DevicePlainService{
		agent:   agent,
		configs: map[string]DeviceConfig{},
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice())
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		if err := deviceConfig.Free(); err != nil {
			return nil, fmt.Errorf("failed to update device and free the unpublished replacement: %w (update error: %v)", err, err)
		}
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	// The update retired the generations holding this service's deferred
	// devices; retry them, then retire the displaced one: freed outright
	// when dangling, parked while a pinned generation still references
	// it.
	m.reclaimDeferred()
	if old, ok := m.configs[name]; ok {
		m.parkOrFree(old)
	}
	m.configs[name] = *deviceConfig

	return &plainpb.UpdateDevicePlainResponse{}, nil
}

// parkOrFree frees the device when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *DevicePlainService) parkOrFree(handle DeviceConfig) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred device, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this service's superseded devices; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *DevicePlainService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *DevicePlainService) reclaimDeferred() {
	kept := m.deferred[:0]
	for idx := range m.deferred {
		if err := m.deferred[idx].Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, m.deferred[idx])
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}

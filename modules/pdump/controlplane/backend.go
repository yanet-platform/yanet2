package pdump

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Settings are the capture parameters a module config is built from.
type Settings struct {
	Filter   string
	Mode     uint32
	Snaplen  uint32
	RingSize uint32
}

// Ring is one worker's capture ring inside the module config memory.
type Ring struct {
	WriteIdx    *uint64
	ReadableIdx *uint64
	Data        []byte
}

// BackendOption configures the shared-memory backend.
type BackendOption func(*backendOptions)

type backendOptions struct {
	Log *zap.Logger
}

func newBackendOptions() *backendOptions {
	return &backendOptions{
		Log: zap.NewNop(),
	}
}

// WithBackendLog sets the logger for the shared-memory backend.
func WithBackendLog(log *zap.Logger) BackendOption {
	return func(o *backendOptions) {
		o.Log = log
	}
}

// backend is the production Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
	log   *zap.Logger
}

// NewBackend creates a backend over the agent's shared memory.
func NewBackend(agent *ffi.Agent, options ...BackendOption) Backend {
	opts := newBackendOptions()
	for _, o := range options {
		o(opts)
	}

	return &backend{
		agent: agent,
		log:   opts.Log,
	}
}

// UpdateModule builds a module config with fresh rings from the settings
// and publishes it.
//
// On error nothing stays allocated.
func (m *backend) UpdateModule(name string, settings Settings) (Module, error) {
	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	rings, err := m.apply(name, config, settings)
	if err != nil {
		if err := config.Free(); err != nil {
			m.log.Error("failed to free unpublished pdump module",
				zap.String("name", name), zap.Error(err))
		}
		return nil, err
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		if err := config.Free(); err != nil {
			m.log.Error("failed to free unpublished pdump module",
				zap.String("name", name), zap.Error(err))
		}
		return nil, fmt.Errorf("failed to update module %s: %w", name, err)
	}

	return &shmModule{config: config, rings: rings}, nil
}

// DeleteModule removes the module config from the dataplane.
func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

// apply writes the settings into an unpublished module config and
// allocates its rings.
func (m *backend) apply(name string, config *ModuleConfig, settings Settings) ([]Ring, error) {
	m.log.Debug("set dump mode", zap.String("module", name))
	if err := config.SetDumpMode(settings.Mode); err != nil {
		return nil, fmt.Errorf("failed to set dump mode for %s: %w", name, err)
	}

	m.log.Debug("set snaplen", zap.String("module", name))
	if err := config.SetSnapLen(settings.Snaplen); err != nil {
		return nil, fmt.Errorf("failed to set snaplen for %s: %w", name, err)
	}

	m.log.Debug("set filter", zap.String("module", name))
	if err := config.SetFilter(settings.Filter); err != nil {
		return nil, fmt.Errorf("failed to set pdump filter for %s: %w", name, err)
	}

	m.log.Debug("setup ring", zap.String("module", name))
	rings, err := config.SetupRings(settings.RingSize)
	if err != nil {
		return nil, fmt.Errorf("failed to setup ring buffers for %s: %w", name, err)
	}

	return rings, nil
}

// shmModule is a published module config with its rings.
type shmModule struct {
	config *ModuleConfig
	rings  []Ring
}

func (m *shmModule) Rings() []Ring {
	return m.rings
}

func (m *shmModule) Free() error {
	return m.config.Free()
}

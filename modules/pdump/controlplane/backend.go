package pdump

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Settings are the capture parameters a module config is built from.
//
// Settings carry the name of the pre-existing ring object the module
// captures into. It is always set on a published config: a config update
// fills it in before the backend ever sees an empty value.
type Settings struct {
	Filter   string
	Mode     uint32
	Snaplen  uint32
	RingName string
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

// UpdateModule builds a module config from the settings, links its ring by
// name and publishes it.
//
// The caller resolves and leases the ring before this call, so
// this only records the link; it does not check the ring exists or fits a
// record. On error nothing stays allocated.
func (m *backend) UpdateModule(name string, settings Settings) (Module, error) {
	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	if err := m.apply(name, config, settings); err != nil {
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

	return &shmModule{config: config}, nil
}

// DeleteModule removes the module config from the dataplane.
//
// It never touches the ring the config was linked to.
func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

// apply writes the settings into an unpublished module config and links
// its ring.
func (m *backend) apply(name string, config *ModuleConfig, settings Settings) error {
	m.log.Debug("set dump mode", zap.String("module", name))
	if err := config.SetDumpMode(settings.Mode); err != nil {
		return fmt.Errorf("failed to set dump mode for %s: %w", name, err)
	}

	m.log.Debug("set snaplen", zap.String("module", name))
	if err := config.SetSnapLen(settings.Snaplen); err != nil {
		return fmt.Errorf("failed to set snaplen for %s: %w", name, err)
	}

	m.log.Debug("set filter", zap.String("module", name))
	if err := config.SetFilter(settings.Filter); err != nil {
		return fmt.Errorf("failed to set pdump filter for %s: %w", name, err)
	}

	m.log.Debug("link ring", zap.String("module", name), zap.String("ring", settings.RingName))
	if err := config.LinkRing(settings.RingName); err != nil {
		return fmt.Errorf("failed to link ring for %s: %w", name, err)
	}

	return nil
}

// shmModule is a published module config.
type shmModule struct {
	config *ModuleConfig
}

func (m *shmModule) Free() error {
	return m.config.Free()
}

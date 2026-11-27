package decap

import (
	"fmt"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decapshm"
)

// moduleType identifies the decap module to the shared-memory agent.
const moduleType = "decap"

// backend is the real Backend implementation backed by shared memory,
// which it builds and publishes without cgo.
type backend struct {
	agent   *agent.Agent
	options []cpconfig.UpdateOption
}

// NewBackend creates a backend that operates on real shared memory
// through the agent.
//
// The update options reach every publication and delete; a dataplane
// whose workers take their contexts themselves needs none.
func NewBackend(owner *agent.Agent, options ...cpconfig.UpdateOption) *backend {
	return &backend{
		agent:   owner,
		options: options,
	}
}

func (m *backend) Update(name string, payload PrefixSets) (crud.Handle, error) {
	module, err := decapshm.NewConfig(m.agent, name, slices.Concat(payload.Prefixes4, payload.Prefixes6))
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", crud.NativeError(err, decapshm.ErrInvalidPrefix))
	}
	handle := &moduleHandle{module: module}

	err = cpconfig.UpdateModules(m.agent.Instance, []*layout.CpModule{module}, m.options...)
	if err != nil {
		err = fmt.Errorf("failed to publish module config: %w", crud.NativeError(err))
		if freeErr := handle.Free(); freeErr != nil {
			return nil, fmt.Errorf("%w; also failed to free the abandoned config: %w", err, freeErr)
		}
		return nil, err
	}

	return handle, nil
}

func (m *backend) Delete(name string) error {
	if err := cpconfig.DeleteModule(m.agent.Instance, moduleType, name, m.options...); err != nil {
		return fmt.Errorf("failed to delete module config type %q name %q: %w", moduleType, name, crud.NativeError(err))
	}
	return nil
}

// moduleHandle owns a decap configuration the backend built until its free
// succeeds.
type moduleHandle struct {
	module *layout.CpModule
}

// Free frees the configuration once no generation holds it.
//
// A refused free reports the configuration as still referenced and
// leaves the handle usable for a retry. Repeated calls after a success
// do nothing.
func (m *moduleHandle) Free() error {
	if m.module == nil {
		return nil
	}
	if err := decapshm.FreeConfig(m.module); err != nil {
		return fmt.Errorf("failed to free module config: %w", crud.NativeError(err))
	}
	m.module = nil
	return nil
}

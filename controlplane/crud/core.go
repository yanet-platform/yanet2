// Package crud owns the published configs of a module or device
// control-plane service.
//
// A service keeps its payload conversion and its extra reads local and
// delegates the publish lifecycle — the config store, the shared-memory
// handle of the published generation and the retirement of the previous
// one — to a Core.
package crud

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Handle is the shared-memory handle of a published config.
type Handle = configstore.Entry

// Backend builds and removes configs in shared memory.
type Backend[P any] interface {
	// Update builds a config from the payload and publishes it to the
	// dataplane, returning the handle of the published generation.
	Update(name string, payload P) (Handle, error)
	// Delete removes the config published under the name.
	Delete(name string) error
}

// Entry pairs an immutable published payload with the handle of its
// shared-memory config.
//
// A mutation must build a fresh entry rather than reuse the one it is
// handed: the published entry reaches readers without a lock.
type Entry[P any] struct {
	// Payload is the published configuration document.
	Payload P
	// Handle is the shared-memory handle of the published generation.
	Handle Handle
}

// Free releases the shared-memory handle held by the entry.
//
// It is safe to call even when no handle is held. The result is the
// handle's: nil when destroyed, ffi.ErrStillReferenced when a live
// generation still references it and the caller must remember it.
func (m *Entry[P]) Free() error {
	if m.Handle == nil {
		return nil
	}
	return m.Handle.Free()
}

// MutationObserver observes the writer-section lifecycle of a mutation.
//
// A mutation reports waiting before it enters the store's writer
// section, acquired once the section holds it — with the shared-memory
// work about to run inside it — and releasing as that work ends. A
// mutation the store rejects before running it, a delete of an unknown
// name, reports only the first phase.
//
// Replace and Mutate report the update operation, Delete the delete
// operation. The observer runs synchronously inside the mutation:
// blocking in the acquired phase holds the name's writer section until
// it returns, which is exactly what a test proving serialization
// pauses on.
type MutationObserver interface {
	ObserveMutation(operation, phase string)
}

// The operations and phases a MutationObserver reports.
const (
	OperationUpdate = "update"
	OperationDelete = "delete"

	PhaseWaiting   = "waiting"
	PhaseAcquired  = "acquired"
	PhaseReleasing = "releasing"
)

// Option configures the Core constructor.
type Option func(*coreOptions)

// coreOptions holds the optional parameters of the Core constructor.
type coreOptions struct {
	Observer MutationObserver
}

func newCoreOptions() *coreOptions {
	return &coreOptions{}
}

// WithMutationObserver installs an observer receiving every mutation's
// writer-section lifecycle.
func WithMutationObserver(observer MutationObserver) Option {
	return func(o *coreOptions) {
		o.Observer = observer
	}
}

// Core owns the published configs of one control-plane service, keyed
// by name.
type Core[P any] struct {
	backend  Backend[P]
	configs  *configstore.Store[*Entry[P]]
	observer MutationObserver
}

// NewCore returns a Core publishing through the given backend.
func NewCore[P any](backend Backend[P], options ...Option) *Core[P] {
	opts := newCoreOptions()
	for _, option := range options {
		option(opts)
	}

	return &Core[P]{
		backend:  backend,
		configs:  configstore.NewStore[*Entry[P]](),
		observer: opts.Observer,
	}
}

// observe reports a mutation lifecycle phase to the observer, when one
// is installed.
func (m *Core[P]) observe(operation, phase string) {
	if m.observer != nil {
		m.observer.ObserveMutation(operation, phase)
	}
}

// List returns the published config names in sorted order.
func (m *Core[P]) List() []string {
	return m.configs.Names()
}

// Get returns the payload published under the name.
func (m *Core[P]) Get(name string) (P, bool) {
	entry, ok := m.configs.Get(name)
	if !ok {
		var zero P
		return zero, false
	}
	return entry.Payload, true
}

// Replace publishes the payload as the whole config under the name,
// retiring the previous generation.
//
// A backend failure is returned unchanged and leaves the store as it
// was; the caller maps it to its gRPC status.
func (m *Core[P]) Replace(name string, payload P) error {
	m.observe(OperationUpdate, PhaseWaiting)
	return m.configs.Update(name, func(*Entry[P], bool) (*Entry[P], error) {
		m.observe(OperationUpdate, PhaseAcquired)
		defer m.observe(OperationUpdate, PhaseReleasing)

		handle, err := m.backend.Update(name, payload)
		if err != nil {
			return nil, err
		}
		return &Entry[P]{Payload: payload, Handle: handle}, nil
	})
}

// Mutate applies an incremental change to the config published under
// the name.
//
// The callback is handed the current payload, or the zero one when the
// name is new, and returns the payload to publish. It must not change
// the payload it is handed in place: the published payload reaches
// readers without a lock. Its error, like a backend failure, is
// returned unchanged and leaves the store as it was.
func (m *Core[P]) Mutate(name string, mutate func(current P, ok bool) (P, error)) error {
	m.observe(OperationUpdate, PhaseWaiting)
	return m.configs.Update(name, func(current *Entry[P], ok bool) (*Entry[P], error) {
		m.observe(OperationUpdate, PhaseAcquired)
		defer m.observe(OperationUpdate, PhaseReleasing)

		var previous P
		if ok {
			previous = current.Payload
		}
		next, err := mutate(previous, ok)
		if err != nil {
			return nil, err
		}
		handle, err := m.backend.Update(name, next)
		if err != nil {
			return nil, err
		}
		return &Entry[P]{Payload: next, Handle: handle}, nil
	})
}

// Delete removes the config published under the name, releasing its
// shared-memory handle.
//
// A backend refusal is returned unchanged and keeps the config
// published; the caller maps it to its gRPC status.
func (m *Core[P]) Delete(name string) error {
	m.observe(OperationDelete, PhaseWaiting)
	return m.configs.Delete(name, func(*Entry[P]) error {
		m.observe(OperationDelete, PhaseAcquired)
		defer m.observe(OperationDelete, PhaseReleasing)

		return m.backend.Delete(name)
	})
}

// DeleteError maps a failed delete to the gRPC status it reports.
//
// Nothing published under the name reports NotFound; a config the
// dataplane refuses to drop reports FailedPrecondition; anything else
// reports Internal.
func DeleteError(name string, err error) error {
	switch {
	case errors.Is(err, configstore.ErrNotFound):
		return status.Errorf(codes.NotFound, "config %q not found", name)
	case errors.Is(err, ffi.ErrFailedPrecondition):
		return status.Errorf(codes.FailedPrecondition, "failed to delete config %q: %v", name, err)
	default:
		return status.Errorf(codes.Internal, "failed to delete config %q: %v", name, err)
	}
}

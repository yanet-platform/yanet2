//go:build cgo

package modulebase

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
)

// NewBase attaches to the shared-memory agent under the module's type
// name.
//
// The returned base carries a logger scoped to the module.
func NewBase(cfg agent.AttachConfig, name, endpoint string, options ...Option) (*Base, error) {
	log := moduleOptions(name, options).Log

	attachment, err := ffi.Attach(cfg, name, log)
	if err != nil {
		return nil, err
	}

	return &Base{
		attachment: attachment,
		name:       name,
		endpoint:   endpoint,
		log:        log,
	}, nil
}

// Agent returns the shared-memory agent the module publishes through,
// or nil for a module attached natively.
func (m *Base) Agent() *ffi.Agent {
	attachment, ok := m.attachment.(*ffi.Attachment)
	if !ok {
		return nil
	}
	return attachment.Agent
}

// Package decap builds decap module configurations from Go, as the C
// decap api does: a module header and two prefix trees the dataplane
// matches the outer destination against.
package decap

import (
	"fmt"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// NewConfig builds a decap configuration in the agent's memory that
// decapsulates packets whose outer destination lies in a prefix.
func NewConfig(owner *agent.Agent, name string, prefixes []netip.Prefix) (*layout.CpModule, error) {
	size := unsafe.Sizeof(layout.DecapModuleConfig{})
	block := shmalloc.ContextAlloc(&owner.Agent.MemoryContext, size)
	if block == nil {
		return nil, fmt.Errorf("allocate decap config: %w", cpconfig.ErrNoMemory)
	}
	config := (*layout.DecapModuleConfig)(block)
	if err := cpconfig.InitModule(&config.CpModule, owner, "decap", name); err != nil {
		shmalloc.ContextFree(&owner.Agent.MemoryContext, block, size)
		return nil, err
	}
	context := &config.CpModule.Entity.MemoryContext
	if err := lpm.Init(&config.Prefixes4, context, "prefixes4"); err != nil {
		return nil, err
	}
	if err := lpm.Init(&config.Prefixes6, context, "prefixes6"); err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		from, to := prefixRange(prefix)
		tree := &config.Prefixes6
		if prefix.Addr().Is4() {
			tree = &config.Prefixes4
		}
		if err := lpm.Insert(tree, from, to, 1); err != nil {
			return nil, fmt.Errorf("insert prefix %s: %w", prefix, err)
		}
	}
	return &config.CpModule, nil
}

// prefixRange returns the first and last addresses a prefix covers, the
// first as the prefix writes it.
func prefixRange(prefix netip.Prefix) ([]byte, []byte) {
	from := prefix.Addr().AsSlice()
	to := prefix.Masked().Addr().AsSlice()
	for bit := prefix.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	return from, to
}

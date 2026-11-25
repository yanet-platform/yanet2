//go:build linux && (amd64 || arm64)

// Package decapshm builds decap module configurations in shared memory
// without cgo, as the C decap api does: a module header and two prefix
// trees the dataplane matches the outer destination address against.
package decapshm

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// moduleType is the dataplane module a decap configuration configures.
const moduleType = "decap"

// configSize is the size of the block a decap configuration takes from
// its agent.
const configSize = unsafe.Sizeof(layout.DecapModuleConfig{})

// ErrInvalidPrefix marks a prefix that names no network.
var ErrInvalidPrefix = errors.New("invalid prefix")

// NewConfig builds a decap configuration in the agent's memory whose
// module decapsulates packets with an outer destination in a prefix.
//
// It takes the cgo control plane's steps: the configuration with empty
// prefix trees, an add per prefix, and the configuration's free when an
// add fails. A name its field cannot hold whole and a prefix naming no
// network are refused before anything is allocated. A failure frees what
// the build took and unlinks its memory contexts; an exhausted agent
// fails with the allocator's out-of-memory error.
func NewConfig(owner *agent.Agent, name string, prefixes []netip.Prefix) (*layout.CpModule, error) {
	if err := cpconfig.CheckModuleNames(moduleType, name); err != nil {
		return nil, err
	}
	for idx, prefix := range prefixes {
		if !prefix.IsValid() {
			return nil, fmt.Errorf("%w %d of decap module %q: %s", ErrInvalidPrefix, idx, name, prefix)
		}
	}

	config, err := newConfig(owner, name)
	if err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		if err := addPrefix(config, prefix); err != nil {
			err = fmt.Errorf("add prefix %s to decap module %q: %w", prefix, name, err)
			return nil, errors.Join(err, FreeConfig(&config.CpModule))
		}
	}
	return &config.CpModule, nil
}

// AddPrefix maps the addresses of a prefix to decapsulation in a decap
// configuration no generation publishes yet, as the C decap api's prefix
// adds do for the prefix's family.
//
// A prefix naming no network is refused before the configuration is
// touched. An exhausted agent fails with the allocator's out-of-memory
// error and leaves a configuration its free still frees, with part of
// the prefix mapped.
func AddPrefix(module *layout.CpModule, prefix netip.Prefix) error {
	if !prefix.IsValid() {
		return fmt.Errorf("%w: %s", ErrInvalidPrefix, prefix)
	}
	return addPrefix(configOf(module), prefix)
}

// FreeConfig frees a decap configuration no generation holds, as
// decap_module_config_free does.
//
// One a generation holds is refused as still referenced, and nothing is
// freed. The release of the agent's hold reclaims the agent when a later
// attach superseded it during the free.
func FreeConfig(module *layout.CpModule) error {
	if err := cpconfig.TryDestroyModule(module); err != nil {
		return err
	}
	owner := module.Entity.Agent.Get()
	destroy(configOf(module))
	cpconfig.DestroyReleased(owner)
	return nil
}

// configOf returns the decap configuration a module header opens.
func configOf(module *layout.CpModule) *layout.DecapModuleConfig {
	offset := unsafe.Offsetof(layout.DecapModuleConfig{}.CpModule)
	return (*layout.DecapModuleConfig)(unsafe.Add(unsafe.Pointer(module), -int(offset)))
}

// newConfig allocates a decap configuration with empty prefix trees in
// the agent's memory, as decap_module_config_new does.
//
// A header that cannot be prepared returns the block; trees that cannot
// be prepared finalise the header and return the block.
func newConfig(owner *agent.Agent, name string) (*layout.DecapModuleConfig, error) {
	context := &owner.Agent.MemoryContext
	config := (*layout.DecapModuleConfig)(shmalloc.ContextAlloc(context, configSize))
	if config == nil {
		return nil, fmt.Errorf("allocate decap module %q: %w", name, shmalloc.ErrNoMemory)
	}
	if err := cpconfig.InitModule(&config.CpModule, owner, moduleType, name); err != nil {
		shmalloc.ContextFree(context, unsafe.Pointer(config), configSize)
		return nil, err
	}
	if err := initData(config, &config.CpModule.Entity.MemoryContext); err != nil {
		cpconfig.FiniModule(&config.CpModule)
		shmalloc.ContextFree(context, unsafe.Pointer(config), configSize)
		return nil, fmt.Errorf("init decap module %q: %w", name, err)
	}
	return config, nil
}

// destroy frees a decap configuration's prefix trees, its header and its
// block, as decap_module_config_destroy does.
func destroy(config *layout.DecapModuleConfig) {
	finiData(config)
	owner := config.CpModule.Entity.Agent.Get()
	cpconfig.FiniModule(&config.CpModule)
	shmalloc.ContextFree(&owner.MemoryContext, unsafe.Pointer(config), configSize)
}

// initData prepares the two empty prefix trees under the module's memory
// context, as decap_module_config_data_init does.
//
// The first tree is freed when the second fails.
func initData(config *layout.DecapModuleConfig, context *layout.MemoryContext) error {
	if err := lpm.Init(&config.Prefixes4, context, "prefixes4"); err != nil {
		return fmt.Errorf("init ipv4 prefix tree: %w", err)
	}
	if err := lpm.Init(&config.Prefixes6, context, "prefixes6"); err != nil {
		lpm.Free(&config.Prefixes4)
		return fmt.Errorf("init ipv6 prefix tree: %w", err)
	}
	return nil
}

// finiData frees both prefix trees, as decap_module_config_data_fini
// does.
func finiData(config *layout.DecapModuleConfig) {
	lpm.Free(&config.Prefixes4)
	lpm.Free(&config.Prefixes6)
}

// addPrefix maps the addresses of a valid prefix to decapsulation in the
// tree of its family, as the C decap api's prefix adds do.
//
// The range starts at the prefix's address as written, host bits
// included, and ends at the last address of its network.
func addPrefix(config *layout.DecapModuleConfig, prefix netip.Prefix) error {
	from := prefix.Addr().AsSlice()
	to := prefix.Masked().Addr().AsSlice()
	for bit := prefix.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	if prefix.Addr().Is4() {
		return lpm.Insert(&config.Prefixes4, from, to, 1)
	}
	return lpm.Insert(&config.Prefixes6, from, to, 1)
}

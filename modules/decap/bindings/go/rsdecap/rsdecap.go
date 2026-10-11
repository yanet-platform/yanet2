//go:build yanet_rust_cp

// Package rsdecap binds the Rust decap control-plane api through the C ABI
// of the yanet-cp static library (rust/sdk-poc-bindgen/yanet-cp).
//
// It is the only Go package that calls that library: arguments are copied
// into C-owned or stack memory, errors come back as a code and a message in
// a caller buffer, and the only pointer kept is the module configuration in
// agent shared memory, owned by the caller exactly like one built by the C
// api. Build with the yanet_rust_cp tag after
// `cargo build --release -p yanet-cp` in rust/sdk-poc-bindgen.
package rsdecap

//#cgo CFLAGS: -I../../../../../ -I../../../../../rust/sdk-poc-bindgen/yanet-cp/include
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -L../../../../../build/lib/controlplane/agent
//#cgo LDFLAGS: -L../../../../../build/lib/errors -L../../../../../build/lib/counters
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -L../../../../../build/lib/dataplane/pipeline
//#cgo LDFLAGS: -Wl,--start-group ${SRCDIR}/../../../../../rust/sdk-poc-bindgen/target/release/libyanet_cp.a -lconfig_cp -lagent -lagent_counters -lerrors -lcounters -lcounter_pattern -lconfig_dp -lpipeline -Wl,--end-group
//#cgo LDFLAGS: -lgcc_s -lpthread -ldl -lm -lrt -lutil
//
//#include <stdlib.h>
//#include "yanet_cp.h"
//#include "lib/controlplane/config/cp_module.h"
//#include "lib/dataplane/module/module.h"
import "C"

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// errBufSize is the capacity of the message buffer passed to the library.
const errBufSize = 512

// abiErr is the result of the start-up ABI check, nil when the library
// matches the header and the C structs this package was compiled with.
var abiErr = checkABI()

func checkABI() error {
	var info C.yanet_cp_abi_info
	if rc := C.yanet_cp_abi_query(&info); rc != C.YANET_CP_OK {
		return fmt.Errorf("yanet-cp ABI query failed: code %d", int(rc))
	}
	checks := []struct {
		name      string
		got, want uint64
	}{
		{"ABI version", uint64(info.abi_version), uint64(C.YANET_CP_ABI_VERSION)},
		{"module ABI version", uint64(info.module_abi_version), uint64(C.YANET_MODULE_ABI_VERSION)},
		{"prefix size", uint64(info.decap_prefix_size), uint64(C.sizeof_struct_yanet_cp_decap_prefix)},
		{"cp_module size", uint64(info.cp_module_size), uint64(C.sizeof_struct_cp_module)},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("yanet-cp library mismatch: %s is %d, this build expects %d", check.name, check.got, check.want)
		}
	}
	return nil
}

// ModuleConfig is a decap module configuration built by the Rust api.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig builds and validates a decap configuration holding the
// given prefixes in the agent's shared memory.
func NewModuleConfig(agent *ffi.Agent, name string, prefixes []netip.Prefix) (*ModuleConfig, error) {
	if abiErr != nil {
		return nil, abiErr
	}

	// Prefixes are copied into C memory: the library reads them during the
	// call and keeps nothing.
	// One record is allocated even for an empty set, so the slice below
	// always covers allocated memory.
	count := len(prefixes)
	capacity := max(count, 1)
	cPrefixes := (*C.struct_yanet_cp_decap_prefix)(C.malloc(C.size_t(capacity) * C.sizeof_struct_yanet_cp_decap_prefix))
	defer C.free(unsafe.Pointer(cPrefixes))
	entries := unsafe.Slice(cPrefixes, capacity)
	for idx, prefix := range prefixes {
		network, ok := xnetip.NetworkFromPrefix(prefix)
		if !ok {
			return nil, errors.New("unsupported prefix: must be either IPv4 or IPv6")
		}
		entry := &entries[idx]
		*entry = C.struct_yanet_cp_decap_prefix{}
		if prefix.Addr().Is4() {
			entry.family = C.YANET_CP_FAMILY_IPV4
			from, to := prefix.Addr().As4(), network.LastAddr().As4()
			copy(unsafe.Slice((*byte)(&entry.from[0]), 4), from[:])
			copy(unsafe.Slice((*byte)(&entry.to[0]), 4), to[:])
		} else {
			entry.family = C.YANET_CP_FAMILY_IPV6
			from, to := prefix.Addr().As16(), network.LastAddr().As16()
			copy(unsafe.Slice((*byte)(&entry.from[0]), 16), from[:])
			copy(unsafe.Slice((*byte)(&entry.to[0]), 16), to[:])
		}
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var errBuf [errBufSize]C.char
	var out unsafe.Pointer
	rc := C.yanet_cp_decap_config_build(
		agent.AsRawPtr(),
		cName,
		cPrefixes,
		C.uintptr_t(count),
		&out,
		&errBuf[0],
		errBufSize,
	)
	if rc != C.YANET_CP_OK {
		return nil, fmt.Errorf("rust decap api: %s (code %d)", C.GoString(&errBuf[0]), int(rc))
	}
	return &ModuleConfig{ptr: ffi.NewModuleConfig(out)}, nil
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free destroys the configuration, or reports ffi.ErrStillReferenced while
// a live generation still holds it. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr unsafe.Pointer
		rc, errno := C.yanet_cp_decap_config_free(ptr, &cErr)
		return int(rc), cErr, errno
	})
}

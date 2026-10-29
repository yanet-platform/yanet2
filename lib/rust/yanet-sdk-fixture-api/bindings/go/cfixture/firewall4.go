//go:build sdk_rust_fixtures

package cfixture

/*
#include <stdlib.h>
#include "api/agent.h"
#include "common/network.h"
typedef struct net4 net4;
#include "yanet_sdk_fixture_api.h"
struct cp_module *yanet_cp_yanet_sdk_fixture_api__firewall4_api_build(
    struct agent *agent, const char *name, const struct Rule4 *rules,
    size_t rules_len, uint64_t fault, yanet_error **err
);
int yanet_cp_yanet_sdk_fixture_api__firewall4_api_free(struct cp_module *module, yanet_error **err);
*/
import "C"

import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

const NativeAllocatorQuantum = int(C.MEMORY_BLOCK_ALLOCATOR_MIN_SIZE)

type Rule4 C.struct_Rule4

// NewRule4 prepares a borrowed IPv4 rule without compiling any classifier.
func NewRule4(source, destination netip.Prefix, actions []byte, counter string) (Rule4, error) {
	var rule Rule4
	if !source.IsValid() || !source.Addr().Is4() || !destination.IsValid() || !destination.Addr().Is4() || len(actions) > 8 {
		return rule, fmt.Errorf("invalid IPv4 rule")
	}
	sourceMask := ^uint32(0) << (32 - source.Bits())
	destinationMask := ^uint32(0) << (32 - destination.Bits())
	sourceBytes := source.Addr().As4()
	destinationBytes := destination.Addr().As4()
	sourceMaskBytes := [4]byte{byte(sourceMask >> 24), byte(sourceMask >> 16), byte(sourceMask >> 8), byte(sourceMask)}
	destinationMaskBytes := [4]byte{byte(destinationMask >> 24), byte(destinationMask >> 16), byte(destinationMask >> 8), byte(destinationMask)}
	copy(bytesOf(&rule.source.addr), sourceBytes[:])
	copy(bytesOf(&rule.source.mask), sourceMaskBytes[:])
	copy(bytesOf(&rule.destination.addr), destinationBytes[:])
	copy(bytesOf(&rule.destination.mask), destinationMaskBytes[:])
	copy(bytesOf(&rule.actions), actions)
	rule.action_count = C.uint64_t(len(actions))
	rule.active = 1
	if err := setName(bytesOf(&rule.counter), counter); err != nil {
		return rule, err
	}
	return rule, nil
}

func (m *Rule4) Disable()     { m.active = 0 }
func (m *Rule4) InvalidMask() { copy(bytesOf(&m.source.mask), []byte{0x55, 0x55, 0x55, 0x55}) }

// NewFirewall4 pins input records only for the synchronous authored API call.
func NewFirewall4(agent *ffi.Agent, name string, rules []Rule4, fault uint64) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var pinner runtime.Pinner
	defer pinner.Unpin()
	records, count := first(&pinner, rules)
	var cErr *C.yanet_error
	ptr := C.yanet_cp_yanet_sdk_fixture_api__firewall4_api_build((*C.struct_agent)(agent.AsRawPtr()), cName, (*C.struct_Rule4)(unsafe.Pointer(records)), count, C.uint64_t(fault), &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("build IPv4 firewall: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return &ModuleConfig{ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)), free: freeFirewall4}, nil
}

func freeFirewall4(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
	var cErr *C.yanet_error
	result, errno := C.yanet_cp_yanet_sdk_fixture_api__firewall4_api_free((*C.struct_cp_module)(ptr), &cErr)
	return int(result), unsafe.Pointer(cErr), errno
}

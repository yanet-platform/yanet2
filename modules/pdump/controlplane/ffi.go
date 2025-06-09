package pdump

//#cgo CFLAGS: -I../../../ -I../dataplane
//#cgo LDFLAGS: -L../../../build/modules/pdump/api -lpdump_cp
//#cgo LDFLAGS: -lpcap
//
//#include <stdlib.h>
//#include "modules/pdump/api/controlplane.h"
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"runtime/cgo"
	"strings"
	"unsafe"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

var (
	logger    *zap.SugaredLogger
	debugEBPF bool

	defaultSnaplen = uint32(C.default_snaplen)
	replacer       = strings.NewReplacer("\n", "\\n")
)

//export pdumpGoControlplaneLog
func pdumpGoControlplaneLog(level C.uint32_t, msg *C.char) {
	if logger == nil {
		return
	}
	goMsg := C.GoString(msg)
	switch level {
	case C.log_emerg, C.log_alert, C.log_crit:
		logger.Errorf("CRIT: %s", replacer.Replace(goMsg))
	case C.log_error:
		logger.Errorf("%s", goMsg) // format for suppressing trace

	case C.log_warn:
		logger.Warn(replacer.Replace(goMsg))
	case C.log_notice, C.log_info:
		logger.Info(replacer.Replace(goMsg))
	case C.log_debug:
		if strings.HasPrefix(goMsg, "BPF: ") && !debugEBPF {
			return
		}
		logger.Debug(replacer.Replace(goMsg))
	}
}

//export goErrorCallback
func goErrorCallback(h C.uintptr_t, msg *C.char) {
	fn := cgo.Handle(h).Value().(func(*C.char))
	fn(msg)
}

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	// Create a new module config using the C API
	ptr, err := C.pdump_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to create module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) SetFilter(filter string) error {
	cFilter := C.CString(filter)
	defer C.free(unsafe.Pointer(cFilter))

	buf := bytes.Buffer{}
	errCallback := cgo.NewHandle(func(msg *C.char) {
		goMsg := C.GoString(msg)
		buf.WriteString(goMsg)
	})
	defer errCallback.Delete()

	rc, err := C.pdump_module_config_set_filter(
		m.asRawPtr(),
		cFilter,
		C.uintptr_t(errCallback),
	)
	if rc != 0 {
		reason := buf.String()
		if reason != "" {
			return errors.Join(err, fmt.Errorf("reason=%s", reason))
		}
		return errors.Join(err, fmt.Errorf("error code=%d", rc))
	}
	return nil
}

func (m *ModuleConfig) SetDumpMode(pbMode pdumppb.DumpMode) error {
	var mode C.enum_pdump_mode
	switch pbMode {
	case pdumppb.DumpMode_PDUMP_DUMP_INPUT:
		mode = C.PDUMP_INPUT
	case pdumppb.DumpMode_PDUMP_DUMP_DROPS:
		mode = C.PDUMP_DROPS
	case pdumppb.DumpMode_PDUMP_DUMP_BOTH:
		mode = C.PDUMP_BOTH
	default:
		return fmt.Errorf("unknown case for C.enum_pdump_mode: %v", pbMode)
	}

	rc, err := C.pdump_module_config_set_mode(
		m.asRawPtr(),
		mode,
	)
	if rc != 0 {
		return errors.Join(fmt.Errorf("error code=%d", rc), err)
	}
	return nil
}

func (m *ModuleConfig) SetSnapLen(snaplen uint32) error {
	buf := bytes.Buffer{}
	errCallback := cgo.NewHandle(func(msg *C.char) {
		goMsg := C.GoString(msg)
		buf.WriteString(goMsg)
	})
	defer errCallback.Delete()

	rc, err := C.pdump_module_config_set_snaplen(
		m.asRawPtr(),
		C.uint32_t(snaplen),
		C.uintptr_t(errCallback),
	)
	if rc != 0 {
		reason := buf.String()
		if reason != "" {
			return errors.Join(err, fmt.Errorf("reason=%s", reason))
		}
		return errors.Join(fmt.Errorf("error code=%d", rc), err)
	}
	return nil
}

func (m *ModuleConfig) SetupRing(ring *ringBuffer, log *zap.SugaredLogger) error {
	var workerCount C.uint64_t

	addr, err := C.pdump_module_config_set_per_worker_ring(
		m.asRawPtr(),
		C.uint32_t(ring.perWorkerSize),
		&workerCount,
	)
	if addr == nil {
		return errors.Join(fmt.Errorf("failed to allocate ring buffer"), err)
	}
	ring.workers = nil // forget about old rings...
	rings := unsafe.Slice(addr, workerCount)
	for idx := range rings {
		dataPtr := C.pdump_module_config_addr_of(&rings[idx].data)
		worker := &workerArea{
			writeIdx:    (*uint64)(&(rings[idx].write_idx)),
			readableIdx: (*uint64)(&(rings[idx].readable_idx)),
			readIdx:     0,
			data:        unsafe.Slice((*byte)(dataPtr), rings[idx].size),
			mask:        uint64(rings[idx].mask),
			log:         log.With("ringIdx", idx).Desugar(),
		}
		ring.workers = append(ring.workers, worker)
	}
	return nil
}

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
)

var (
	logger    *zap.SugaredLogger
	debugEBPF bool

	defaultSnaplen = uint32(C.default_snaplen)
	replacer       = strings.NewReplacer("\n", "\\n")

	defaultMode uint32 = C.PDUMP_INPUT
	maxMode     uint32 = C.PDUMP_ALL
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

// errorCallbackContext manages the lifecycle of a CGO error callback handle.
// It captures error messages from C code and provides them as a string.
type errorCallbackContext struct {
	buf    bytes.Buffer
	handle cgo.Handle
}

// newErrorCallbackContext creates a new error callback context.
// The returned context must be closed with Close() to free the CGO handle.
func newErrorCallbackContext() *errorCallbackContext {
	ctx := &errorCallbackContext{}
	ctx.handle = cgo.NewHandle(func(msg *C.char) {
		goMsg := C.GoString(msg)
		ctx.buf.WriteString(goMsg)
	})
	return ctx
}

// Handle returns the CGO handle that can be passed to C functions.
func (e *errorCallbackContext) Handle() C.uintptr_t {
	return C.uintptr_t(e.handle)
}

// Reason returns the accumulated error messages from C code.
func (e *errorCallbackContext) Reason() string {
	return e.buf.String()
}

// Close releases the CGO handle. Must be called to prevent handle leaks.
func (e *errorCallbackContext) Close() {
	e.handle.Delete()
}

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	// Create a new module config using the C API
	ptr, err := C.pdump_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
	if ptr == nil {
		return nil, errors.Join(fmt.Errorf("failed to create module config: module %q not found", name), err)
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

// Free frees the pdump module configuration
func (m *ModuleConfig) Free() {
	C.pdump_module_config_free(m.asRawPtr())
}

func (m *ModuleConfig) SetFilter(filter string) error {
	cFilter := C.CString(filter)
	defer C.free(unsafe.Pointer(cFilter))

	errCtx := newErrorCallbackContext()
	defer errCtx.Close()

	rc, err := C.pdump_module_config_set_filter(
		m.asRawPtr(),
		cFilter,
		errCtx.Handle(),
	)
	if rc != 0 {
		if reason := errCtx.Reason(); reason != "" {
			return errors.Join(err, fmt.Errorf("reason=%s", reason))
		}
		return errors.Join(err, fmt.Errorf("error code=%d", rc))
	}
	return nil
}

func (m *ModuleConfig) SetDumpMode(pbMode uint32) error {
	if pbMode > C.PDUMP_ALL {
		return fmt.Errorf("unknown pdump mode %x (max known %x)", pbMode, C.PDUMP_ALL)
	}

	var mode C.enum_pdump_mode
	if pbMode&C.PDUMP_INPUT != 0 {
		mode |= C.PDUMP_INPUT
	}
	if pbMode&C.PDUMP_DROPS != 0 {
		mode |= C.PDUMP_DROPS
	}

	if pbMode != uint32(mode) {
		// This check validates the exhaustiveness of the preceding if
		// statements against the pdump_mode enum.
		// This check will fail if new modes are added.
		return fmt.Errorf("unknown pdump mode %x", pbMode^mode)
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
	errCtx := newErrorCallbackContext()
	defer errCtx.Close()

	rc, err := C.pdump_module_config_set_snaplen(
		m.asRawPtr(),
		C.uint32_t(snaplen),
		errCtx.Handle(),
	)
	if rc != 0 {
		if reason := errCtx.Reason(); reason != "" {
			return errors.Join(err, fmt.Errorf("reason=%s", reason))
		}
		return errors.Join(fmt.Errorf("error code=%d", rc), err)
	}
	return nil
}

func (m *ModuleConfig) SetupRing(ring *ringBuffer, log *zap.SugaredLogger) error {
	var workerCount C.uint64_t

	errCtx := newErrorCallbackContext()
	defer errCtx.Close()

	addr, err := C.pdump_module_config_set_per_worker_ring(
		m.asRawPtr(),
		C.uint32_t(ring.perWorkerSize),
		&workerCount,
		errCtx.Handle(),
	)
	if addr == nil {
		err = errors.Join(fmt.Errorf("failed to allocate ring buffer"), err)
		if reason := errCtx.Reason(); reason != "" {
			err = errors.Join(err, fmt.Errorf("reason=%s", reason))
		}
		return err
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

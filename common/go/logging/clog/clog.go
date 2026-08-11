// Package clog routes lib/logging's C LOG() output through a zap.Logger and
// pushes zap's configured level into the C library's cheap level gate.
//
// zap remains the authority: the C gate is only a prefilter that skips
// formatting and the cgo transition for lines zap would drop anyway, so it
// must never end up stricter than zap's own level.
package clog

/*
#cgo CFLAGS: -I../../../.. -I../../../../build
#cgo LDFLAGS: -L../../../../build/lib/logging/ -llogging

#include "sink.h"
*/
import "C"

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// currentLogger is the destination for C log lines.
//
// The C sink callback may run concurrently with Attach, so it is always
// read atomically and a nil value is a silent no-op rather than a panic.
var currentLogger atomic.Pointer[zap.Logger]

// setLevelMu serializes SetLevel against itself.
//
// SetLevel walks the C gate's levels in two loops, enabling one range and
// disabling the rest. Without this lock, two overlapping calls (gRPC serves
// UpdateLevel concurrently) could interleave those loops and leave the gate
// representing neither requested level. Level changes are rare and
// human-driven, so a mutex is cheap enough and needs no C-side change.
var setLevelMu sync.Mutex

// Attach makes log the destination for the C library's LOG() calls.
//
// It installs a process-global C sink pointer through a plain,
// unsynchronized store, so callers must install it exactly once during
// bootstrap, before any C code can log and before that pointer can be read
// concurrently. Production relies on this: it calls Attach once at startup,
// ahead of the gateway serving requests.
func Attach(log *zap.Logger) {
	currentLogger.Store(log)
	C.clog_install_sink()
}

// SetLevel pushes level into the C library's gate.
//
// It enables the mapped minimum through ERROR before disabling everything
// below it, so no level that should stay enabled is ever momentarily off.
// Concurrent calls are serialized against each other, so two overlapping
// updates cannot interleave into a gate state matching neither.
func SetLevel(level zapcore.Level) {
	setLevelMu.Lock()
	defer setLevelMu.Unlock()

	minLevel := cMinLevel(level)

	for lid := minLevel; lid <= C.ERROR; lid++ {
		C.log_enable_id(C.enum_log_id(lid))
	}
	for lid := minLevel - 1; lid >= C.int(C.TRACE); lid-- {
		C.log_disable_id(C.enum_log_id(lid))
	}
}

// cMinLevel maps a zap level to the lowest C log_id that must stay enabled
// for that level.
func cMinLevel(level zapcore.Level) C.int {
	switch level {
	case zapcore.DebugLevel:
		return C.TRACE
	case zapcore.InfoLevel:
		return C.INFO
	case zapcore.WarnLevel:
		return C.WARN
	default:
		// Error and anything more severe collapse onto the C library's
		// most severe level.
		return C.ERROR
	}
}

// zapLevel maps a C log_id to the zap level a line routed through the sink
// is reported at.
func zapLevel(level C.enum_log_id) zapcore.Level {
	switch level {
	case C.TRACE, C.DEBUG:
		return zapcore.DebugLevel
	case C.INFO:
		return zapcore.InfoLevel
	case C.WARN:
		return zapcore.WarnLevel
	default:
		return zapcore.ErrorLevel
	}
}

// clogSink is installed as the C sink and receives the bare formatted
// message plus the C source location for every enabled LOG() call.
//
// It bypasses Logger.Log, which cannot override the caller, and instead
// checks and writes the entry directly through the logger's core so the C
// file and line land in the caller column next to Go call sites.
//
//export clogSink
func clogSink(level C.enum_log_id, file *C.char, line C.int, msg *C.char, ctx unsafe.Pointer) {
	log := currentLogger.Load()
	if log == nil {
		return
	}

	entry := zapcore.Entry{
		Level:   zapLevel(level),
		Time:    time.Now(),
		Message: C.GoString(msg),
		Caller: zapcore.EntryCaller{
			Defined: true,
			File:    C.GoString(file),
			Line:    int(line),
		},
	}
	if ce := log.Core().Check(entry, nil); ce != nil {
		ce.Write()
	}
}

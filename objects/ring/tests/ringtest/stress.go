package ringtest

//#include "objects/ring/tests/ringtest_writer.h"
import "C"

import (
	"encoding/binary"
	"errors"
)

// Stress runs the C writer at full speed on its own OS thread.
//
// The writer publishes by the ring's publish batch. Each record's length and
// bytes come from its sequence number, so a reader can detect a torn record.
type Stress struct {
	s *C.struct_ringtest_stress
}

// StartStress starts writing the given number of records to the writer's
// worker ring.
//
// The ring publishes them by its own publish batch. The writer stops early
// if the ring refuses a record.
func (m *Writer) StartStress(records uint64) (*Stress, error) {
	s := C.ringtest_stress_start(m.worker, m.data, C.uint64_t(records))
	if s == nil {
		return nil, errors.New("failed to start the stress writer thread")
	}
	return &Stress{s: s}, nil
}

// Done reports whether the writer thread has finished.
func (m *Stress) Done() bool {
	return C.ringtest_stress_done(m.s) != 0
}

// Written returns the number of records the writer committed.
//
// The value is final only after the writer has finished.
func (m *Stress) Written() uint64 {
	return uint64(C.ringtest_stress_written(m.s))
}

// Wait joins the writer thread and releases its state.
func (m *Stress) Wait() {
	C.ringtest_stress_join(m.s)
	m.s = nil
}

// StressRecordValid reports whether a payload is exactly the one the stress
// writer wrote for the given sequence number.
func StressRecordValid(seqno uint32, payload []byte) bool {
	if uint32(len(payload)) != uint32(C.ringtest_stress_len(C.uint32_t(seqno))) {
		return false
	}
	for k := 0; k+4 <= len(payload); k += 4 {
		want := uint32(C.ringtest_stress_word(C.uint32_t(seqno), C.uint32_t(k/4)))
		if binary.LittleEndian.Uint32(payload[k:k+4]) != want {
			return false
		}
	}
	return true
}

package testutils

import "sync/atomic"

// FreeSequence is a fake handle whose Free returns the scripted outcomes
// in order and succeeds once they run out, counting every call.
//
// A test scripts a refusal with whichever sentinel the code under test
// matches, so the fake carries no error vocabulary of its own. Calls
// past the script succeed, mirroring a real handle, whose repeated free
// is a no-op.
type FreeSequence struct {
	outcomes []error
	calls    atomic.Int64
	freed    atomic.Int64
}

// NewFreeSequence returns a handle that answers Free with the outcomes in
// order.
func NewFreeSequence(outcomes ...error) *FreeSequence {
	return &FreeSequence{outcomes: outcomes}
}

// Free returns the next scripted outcome.
func (m *FreeSequence) Free() error {
	call := int(m.calls.Add(1))
	if call <= len(m.outcomes) && m.outcomes[call-1] != nil {
		return m.outcomes[call-1]
	}
	m.freed.Add(1)
	return nil
}

// Calls returns how many times Free was called.
func (m *FreeSequence) Calls() int64 {
	return m.calls.Load()
}

// Freed returns how many Free calls succeeded.
func (m *FreeSequence) Freed() int64 {
	return m.freed.Load()
}

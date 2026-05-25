package balancer2

import (
	"iter"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
)

// SessionsState owns a balancer session table and its table chain.
// mu serializes IterSessions against Free so the underlying C resources
// are not accessed after release.
type SessionsState struct {
	name    string
	st      *cbalancer2.SessionTable
	stChain *cbalancer2.SessionTableChain
	agent   *ffi.Agent
	mu      sync.RWMutex
}

func NewSessionsState(name string, agent *ffi.Agent, capacity uint64) (*SessionsState, error) {
	st, err := cbalancer2.NewSessionTable(agent, capacity)
	if err != nil {
		return nil, err
	}
	stChain, err := cbalancer2.NewSessionTableChain(agent, st)
	if err != nil {
		st.Free(agent)
		return nil, err
	}
	return &SessionsState{
		name:    name,
		st:      st,
		stChain: stChain,
		agent:   agent,
	}, nil
}

func (m *SessionsState) Name() string {
	return m.name
}

func (m *SessionsState) Free() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stChain != nil {
		m.stChain.Free(m.agent)
		m.stChain = nil
	}
	if m.st != nil {
		m.st.Free(m.agent)
		m.st = nil
	}
}

// IterSessions yields the live sessions in the table relative to now.
//
// Iteration holds an internal read-lock for the duration of the returned
// sequence: callers must drain or stop iterating promptly to allow Free to
// proceed. If Free has already run, the returned sequence is empty.
func (m *SessionsState) IterSessions(
	now time.Time,
) iter.Seq2[cbalancer2.SessionID, cbalancer2.SessionState] {
	return func(yield func(cbalancer2.SessionID, cbalancer2.SessionState) bool) {
		m.mu.RLock()
		defer m.mu.RUnlock()

		if m.st == nil {
			return
		}
		for id, state := range m.st.Iter(now) {
			if !yield(id, state) {
				return
			}
		}
	}
}

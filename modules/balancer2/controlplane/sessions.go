package balancer2

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
)

type SessionsState struct {
	name    string
	st      *cbalancer2.SessionTable
	stChain *cbalancer2.SessionTableChain
	agent   *ffi.Agent
}

func NewSessionsState(agent *ffi.Agent, name string, capacity uint64) (*SessionsState, error) {
	st, err := cbalancer2.NewSessionTable(agent, capacity)
	if err != nil {
		return nil, err
	}
	stChain, err := cbalancer2.NewSessionTableChain(agent, st)
	if err != nil {
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
	m.stChain.Free(m.agent)
	m.st.Free(m.agent)
}

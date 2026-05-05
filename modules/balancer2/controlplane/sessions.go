package balancer2

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
)

type SessionsState struct {
	st      *cbalancer2.SessionTable
	stChain *cbalancer2.SessionTableChain
	agent   *ffi.Agent
}

func NewSessionsState(agent *ffi.Agent, capacity uint64) (*SessionsState, error) {
	st, err := cbalancer2.NewSessionTable(agent, capacity)
	if err != nil {
		return nil, fmt.Errorf("failed to create session table", err)
	}
	stChain, err := cbalancer2.NewSessionTableChain(agent, st)
	if err != nil {
		return nil, fmt.Errorf("failed create sessions state", err)
	}
	return &SessionsState{
		st:      st,
		stChain: stChain,
		agent:   agent,
	}, nil
}

func (m *SessionsState) Free() {
	m.stChain.Free(m.agent)
	m.st.Free(m.agent)
}

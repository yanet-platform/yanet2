package proxy

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"go.uber.org/zap"
)

type ProxyState struct {
	agent   *ffi.Agent
	cHandle ModuleStatePtr
	log     *zap.SugaredLogger
}

func NewProxyState(
	agent *ffi.Agent,
	config *ProxyConfig,
	log *zap.SugaredLogger,
) (*ProxyState, error) {
	if config.ConnTableSize == 0 {
		return nil, fmt.Errorf("connections table size is 0")
	}

	state, err := NewModuleState(agent, config)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create new module config state: %w",
			err,
		)
	}

	s := &ProxyState{
		agent:   agent,
		cHandle: state,
		log:     log,
	}

	return s, nil
}

func (s *ProxyState) Free() {
	s.cHandle.Free()
}

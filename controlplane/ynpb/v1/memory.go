package ynpb

import (
	"errors"
	"fmt"
	"strings"
)

// MaxAgentNameLen mirrors the C agent name buffer size, including the
// terminating NUL.
const MaxAgentNameLen = 80

func (m *ExtendAgentRequest) Validate() error {
	agent := m.GetAgent()
	if agent == "" {
		return errors.New("agent is required")
	}
	if strings.IndexByte(agent, 0) != -1 {
		return errors.New("agent must not contain NUL")
	}
	if len(agent) >= MaxAgentNameLen {
		return fmt.Errorf("agent must be shorter than %d bytes", MaxAgentNameLen)
	}
	if m.GetSize() == 0 {
		return errors.New("size must be positive")
	}

	return nil
}

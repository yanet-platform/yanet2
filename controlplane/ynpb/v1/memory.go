package ynpb

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// MaxAgentNameLen mirrors the C agent name buffer size, including the
// terminating NUL.
const MaxAgentNameLen = 80

// AgentSizeAlignment mirrors the C allocator's extension granularity.
const AgentSizeAlignment = 8

// MaxAgentExtensionSize is the largest extension that can round up safely.
const MaxAgentExtensionSize uint64 = math.MaxUint64 - (AgentSizeAlignment - 1)

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
	size := m.GetSize()
	if size == 0 {
		return errors.New("size must be positive")
	}
	if size > MaxAgentExtensionSize {
		return fmt.Errorf("size %d must be in range 1..%d", size, MaxAgentExtensionSize)
	}

	return nil
}

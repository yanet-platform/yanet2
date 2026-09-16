package ynpb

import "errors"

func (m *ExtendAgentRequest) Validate() error {
	if m.GetAgent() == "" {
		return errors.New("agent is required")
	}
	if m.GetSize() == 0 {
		return errors.New("size must be positive")
	}

	return nil
}

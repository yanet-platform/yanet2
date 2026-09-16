package ynpb

import "fmt"

func (m *UpdateLevelRequest) Validate() error {
	switch m.GetLevel() {
	case LogLevel_DEBUG, LogLevel_INFO, LogLevel_WARN, LogLevel_ERROR:
		return nil
	default:
		return fmt.Errorf("level unknown value %d", m.GetLevel())
	}
}

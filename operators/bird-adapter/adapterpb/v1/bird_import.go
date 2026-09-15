package adapterpb

import "errors"

// Validate checks that the import configuration and both MPLS source addresses
// are present.
func (m *SetupConfigRequest) Validate() error {
	if m.GetConfig() == nil {
		return errors.New("config is required")
	}
	if m.GetSourceV4() == nil {
		return errors.New("source_v4 is required")
	}
	if m.GetSourceV6() == nil {
		return errors.New("source_v6 is required")
	}

	return nil
}

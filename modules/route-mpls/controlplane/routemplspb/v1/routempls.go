package routemplspb

import (
	"errors"
	"fmt"
)

// maxMPLSLabel is the highest value representable by the dataplane's 20-bit
// label field.
const maxMPLSLabel = 1<<20 - 1

func (m *CreateConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	for idx, rule := range m.GetRules() {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *UpdateConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	for idx, update := range m.GetUpdates() {
		if err := update.Validate(); err != nil {
			return fmt.Errorf("updates[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *UpdateEvent) Validate() error {
	if update := m.GetUpdate(); update != nil {
		if err := update.Validate(); err != nil {
			return fmt.Errorf("update: %w", err)
		}
		return nil
	}
	if withdraw := m.GetWithdraw(); withdraw != nil {
		if err := withdraw.Validate(); err != nil {
			return fmt.Errorf("withdraw: %w", err)
		}
		return nil
	}

	return errors.New("event is required")
}

func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *Rule) Validate() error {
	if nexthop := m.GetNexthop(); nexthop != nil {
		if err := nexthop.Validate(); err != nil {
			return fmt.Errorf("nexthop: %w", err)
		}
	}

	return nil
}

func (m *NextHop) Validate() error {
	label := m.GetLabel()
	if label > maxMPLSLabel {
		return fmt.Errorf("label %d must be in range 0..%d", label, maxMPLSLabel)
	}

	return nil
}

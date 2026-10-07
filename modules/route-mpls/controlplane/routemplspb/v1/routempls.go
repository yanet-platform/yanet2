package routemplspb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxMPLSLabel is the highest value representable by the dataplane's 20-bit
// label field.
const maxMPLSLabel = 1<<20 - 1

func (m *CreateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	for idx, rule := range m.GetRules() {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *UpdateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
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
		if err := validateWithdraw(withdraw); err != nil {
			return fmt.Errorf("withdraw: %w", err)
		}
		return nil
	}

	return errors.New("event is required")
}

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *Rule) Validate() error {
	if m.GetPrefix() == nil {
		return errors.New("prefix is required")
	}
	if m.GetNexthop() == nil {
		return errors.New("nexthop is required")
	}
	if err := m.GetNexthop().Validate(); err != nil {
		return fmt.Errorf("nexthop: %w", err)
	}

	return nil
}

func validateWithdraw(m *Rule) error {
	return m.validateWithdraw()
}

func (m *Rule) validateWithdraw() error {
	if m.GetPrefix() == nil {
		return errors.New("prefix is required")
	}
	if m.GetNexthop() == nil {
		return errors.New("nexthop is required")
	}
	if err := validateLabel(m.GetNexthop()); err != nil {
		return fmt.Errorf("nexthop: %w", err)
	}

	return nil
}

func (m *NextHop) Validate() error {
	if err := validateLabel(m); err != nil {
		return err
	}

	counter := m.GetCounter()
	if strings.IndexByte(counter, 0) != -1 {
		return errors.New("counter must not contain NUL")
	}
	if len(counter) >= commonpb.MaxCounterNameLen {
		return fmt.Errorf("counter must be shorter than %d bytes", commonpb.MaxCounterNameLen)
	}

	return nil
}

func validateLabel(m *NextHop) error {
	return m.validateLabel()
}

func (m *NextHop) validateLabel() error {
	label := m.GetLabel()
	if label > maxMPLSLabel {
		return fmt.Errorf("label %d must be in range 0..%d", label, maxMPLSLabel)
	}

	return nil
}

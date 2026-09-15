package forwardpb

import (
	"errors"
	"fmt"

	"github.com/yanet-platform/yanet2/common/go/xproto"
)

func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *UpdateConfigRequest) Validate() error {
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

func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *Rule) Validate() error {
	if m.GetAction() == nil {
		return errors.New("action is required")
	}

	return nil
}

// MarshalJSON renders the mode by its declared name, or by its number when
// the value is not declared.
func (m ForwardMode) MarshalJSON() ([]byte, error) {
	return xproto.MarshalEnumJSON(m)
}

// UnmarshalJSON accepts the declared name or the number.
func (m *ForwardMode) UnmarshalJSON(data []byte) error {
	return xproto.UnmarshalEnumJSON(data, m)
}

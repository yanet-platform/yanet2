package forwardpb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
)

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *UpdateConfigRequest) Validate() error {
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

func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
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

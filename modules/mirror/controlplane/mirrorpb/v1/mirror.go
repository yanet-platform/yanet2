package mirrorpb

import (
	"errors"
	"fmt"
	"strings"

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

	rules := m.GetRules()
	if len(rules) == 0 {
		return errors.New("rules must contain at least one rule")
	}

	for idx, rule := range rules {
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
	if err := m.GetAction().Validate(); err != nil {
		return fmt.Errorf("action: %w", err)
	}

	return nil
}

func (m *Action) Validate() error {
	target := m.GetTarget()
	if strings.IndexByte(target, 0) != -1 {
		return errors.New("target must not contain NUL")
	}
	if len(target) >= commonpb.MaxDeviceNameLen {
		return fmt.Errorf("target must be shorter than %d bytes", commonpb.MaxDeviceNameLen)
	}

	if _, ok := MirrorMode_name[int32(m.GetMode())]; !ok {
		return fmt.Errorf("mode unknown value %d", m.GetMode())
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

// MarshalJSON renders the mode by its declared name, or by its number when
// the value is not declared.
func (m MirrorMode) MarshalJSON() ([]byte, error) {
	return xproto.MarshalEnumJSON(m)
}

// UnmarshalJSON accepts the declared name or the number.
func (m *MirrorMode) UnmarshalJSON(data []byte) error {
	return xproto.UnmarshalEnumJSON(data, m)
}

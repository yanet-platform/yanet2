package pdumppb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// MaxMode mirrors the largest bitmap accepted by the pdump dataplane.
const MaxMode = 3

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *SetConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	config := m.GetConfig()
	if config == nil {
		return errors.New("config is required")
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	return nil
}

func (m *Config) Validate() error {
	if m.Mode != nil && m.GetMode() > MaxMode {
		return fmt.Errorf("mode %d must be in range 0..%d", m.GetMode(), MaxMode)
	}
	if m.Snaplen != nil && m.GetSnaplen() == 0 {
		return errors.New("snaplen must be greater than zero")
	}
	if m.RingName != nil {
		if err := ringpb.ValidateRingName("ring_name", m.GetRingName()); err != nil {
			return err
		}
	}
	if strings.ContainsRune(m.GetFilter(), '\x00') {
		return errors.New("filter must not contain NUL")
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *ReadDumpRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

package pdumppb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// MaxMode mirrors the largest bitmap accepted by the pdump dataplane.
const MaxMode = 3

const minRingSize = 1 << 20

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
	if config.Mode != nil && config.GetMode() > MaxMode {
		return fmt.Errorf("mode %d must be in range 0..%d", config.GetMode(), MaxMode)
	}
	if config.Snaplen != nil && config.GetSnaplen() == 0 {
		return errors.New("snaplen must be greater than zero")
	}
	if config.RingSize != nil {
		ringSize := config.GetRingSize()
		if ringSize&(ringSize-1) != 0 {
			return fmt.Errorf("ring_size %d must be a power of two", ringSize)
		}
		if ringSize < minRingSize || ringSize > MaxRingSize {
			return fmt.Errorf(
				"ring_size %d must be in range %d..%d",
				ringSize,
				minRingSize,
				MaxRingSize,
			)
		}
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *ReadDumpRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

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
	if m.GetUpdateMask() == nil {
		return errors.New("update_mask is required")
	}

	for _, path := range m.GetUpdateMask().GetPaths() {
		switch path {
		case "filter":
		case "mode":
			mode := config.GetMode()
			if mode > MaxMode {
				return fmt.Errorf("mode %d must be in range 0..%d", mode, MaxMode)
			}
		case "snaplen":
			if config.GetSnaplen() == 0 {
				return errors.New("snaplen must be greater than zero")
			}
		case "ring_size":
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
		default:
			return fmt.Errorf("unknown path '%s'", path)
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

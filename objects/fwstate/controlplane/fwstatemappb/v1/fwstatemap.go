package fwstatemappb

import (
	"errors"
	"fmt"
	"strings"
)

// MaxMapNameLen is the C object-name buffer size, including the terminating
// NUL. The longest accepted name is one byte shorter than this bound.
const MaxMapNameLen = 80

// Per-worker stash buffer bounds a map accepts, in bytes. They mirror the C
// FWSTATE_STASH_MIN_SIZE (one sync record) and FWSTATE_STASH_MAX_SIZE.
const (
	MinStashSize = 62
	MaxStashSize = 1 << 20
)

// ValidateMapName validates a standalone map request name and keeps the
// historical generic map-name field text.
func ValidateMapName(name string) error {
	return ValidateMapNameField("map name", name)
}

// ValidateMapNameField validates a map name while preserving its proto field
// name in every error.
func ValidateMapNameField(field, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.IndexByte(name, 0) != -1 {
		return fmt.Errorf("%s must not contain NUL", field)
	}
	if len(name) >= MaxMapNameLen {
		return fmt.Errorf("%s must be shorter than %d bytes", field, MaxMapNameLen)
	}
	return nil
}

func (m *CreateMapRequest) Validate() error {
	if err := ValidateMapNameField("name", m.GetName()); err != nil {
		return err
	}
	if kind := m.GetKind(); kind != Kind_V4 && kind != Kind_V6 {
		return fmt.Errorf("kind unknown value %d", kind)
	}
	if size := m.GetStashSize(); size != 0 && size < MinStashSize {
		return fmt.Errorf(
			"stash_size %d is below the minimum %d",
			size, MinStashSize,
		)
	}
	if size := m.GetStashSize(); size > MaxStashSize {
		return fmt.Errorf(
			"stash_size %d exceeds maximum allowed value %d",
			size, MaxStashSize,
		)
	}
	return nil
}

func (m *DeleteMapRequest) Validate() error {
	return ValidateMapNameField("name", m.GetName())
}

func (m *GetMapStatsRequest) Validate() error {
	return ValidateMapNameField("name", m.GetName())
}

func (m *InsertLayerRequest) Validate() error {
	return ValidateMapNameField("name", m.GetName())
}

func (m *ListEntriesRequest) Validate() error {
	if err := ValidateMapNameField("map_name", m.GetMapName()); err != nil {
		return err
	}

	direction := m.GetDirection()
	if direction != Direction_FORWARD && direction != Direction_BACKWARD {
		return fmt.Errorf("direction unknown value %d", direction)
	}
	if direction == Direction_FORWARD && m.GetIndex() < 0 {
		return errors.New("index must not be negative for forward reads")
	}
	return nil
}

package filterpb

import (
	"errors"
	"fmt"
	"strings"
)

// MaxDeviceNameLen mirrors the C filter device name buffer size, including
// the terminating NUL.
const MaxDeviceNameLen = 80

func (m *Device) Validate() error {
	name := m.GetName()
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("name must not contain NUL")
	}
	if len(name) >= MaxDeviceNameLen {
		return fmt.Errorf("name must be shorter than %d bytes", MaxDeviceNameLen)
	}

	return nil
}

package commonpb

import (
	"fmt"
)

// Validate validates the TargetModule and returns the config name.
func (x *TargetModule) Validate() (string, error) {
	if x == nil {
		return "", fmt.Errorf("target module cannot be nil")
	}
	name := x.GetConfigName()
	if name == "" {
		return "", fmt.Errorf("target module name is required")
	}

	return name, nil
}

// Validate validates the TargetDevice and returns the device name.
func (x *TargetDevice) Validate() (string, error) {
	if x == nil {
		return "", fmt.Errorf("target device cannot be nil")
	}
	name := x.GetName()
	if name == "" {
		return "", fmt.Errorf("target device name is required")
	}

	return name, nil
}

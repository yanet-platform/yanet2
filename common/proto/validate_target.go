package commonpb

import (
	"fmt"
)

func (x *TargetModule) Validate(instanceMax uint32) (string, uint32, error) {
	if x == nil {
		return "", 0, fmt.Errorf("target module cannot be nil")
	}
	name := x.GetConfigName()
	if name == "" {
		return "", 0, fmt.Errorf("target module name is required")
	}
	inst := x.GetDataplaneInstance()
	if inst >= instanceMax {
		return "", 0, fmt.Errorf("instance index %d for config %s is out of range [0..%d) ", inst, name, instanceMax)
	}

	return name, inst, nil
}

func (x *TargetDevice) Validate(instanceMax uint32) (string, uint32, error) {
	if x == nil {
		return "", 0, fmt.Errorf("target device cannot be nil")
	}
	name := x.GetName()
	if name == "" {
		return "", 0, fmt.Errorf("target device name is required")
	}
	inst := x.GetInstance()
	if inst >= instanceMax {
		return "", 0, fmt.Errorf("instance index %d for config %s is out of range [0..%d) ", inst, name, instanceMax)
	}

	return name, inst, nil
}

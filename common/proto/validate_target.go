package commonpb

import (
	"fmt"
)

func (x *TargetModule) Validate(numaMax uint32) (string, uint32, error) {
	if x == nil {
		return "", 0, fmt.Errorf("target module cannot be nil")
	}
	name := x.GetConfigName()
	if name == "" {
		return "", 0, fmt.Errorf("target module name is required")
	}
	numa := x.GetNuma()
	if numa >= numaMax {
		return "", 0, fmt.Errorf("NUMA index %d for config %s is out of range [0..%d) ", numa, name, numaMax)
	}

	return name, numa, nil
}

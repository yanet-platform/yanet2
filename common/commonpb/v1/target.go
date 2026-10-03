package commonpb

import (
	"fmt"
	"strings"
)

// MaxFunctionNameLen mirrors the C function name buffer size, including the
// terminating NUL.
const MaxFunctionNameLen = 80

// MaxModuleTypeLen mirrors the C module type buffer size, including the
// terminating NUL.
const MaxModuleTypeLen = 80

func (m *FunctionId) Validate() error {
	return validateTargetName("name", m.GetName(), MaxFunctionNameLen)
}

func (m *ModuleId) Validate() error {
	if err := validateTargetName("type", m.GetType(), MaxModuleTypeLen); err != nil {
		return err
	}
	return ValidateModuleName("name", m.GetName())
}

func validateTargetName(field, name string, limit int) error {
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.IndexByte(name, 0) != -1 {
		return fmt.Errorf("%s must not contain NUL", field)
	}
	if len(name) >= limit {
		return fmt.Errorf("%s must be shorter than %d bytes", field, limit)
	}
	return nil
}

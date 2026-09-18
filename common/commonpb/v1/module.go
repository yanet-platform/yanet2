package commonpb

import (
	"fmt"
	"strings"
)

// MaxModuleNameLen mirrors the C module config name buffer size, including the
// terminating NUL.
const MaxModuleNameLen = 80

// ValidateModuleName checks that a module config name is present and fits the
// C name buffer.
func ValidateModuleName(field, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.IndexByte(name, 0) != -1 {
		return fmt.Errorf("%s must not contain NUL", field)
	}
	if len(name) >= MaxModuleNameLen {
		return fmt.Errorf("%s must be shorter than %d bytes", field, MaxModuleNameLen)
	}

	return nil
}

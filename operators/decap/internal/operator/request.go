package operator

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/common/go/xproto"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// LoadModuleConfig reads a module config file, the decap update request in
// YAML, and binds it to the config name the function references.
//
// A file may omit the name, in which case the function's module name is
// used. A file naming a different config is rejected rather than rebound,
// because the function chain would then reference a config this operator
// never pushes.
func LoadModuleConfig(path string, name string) (*decappb.UpdateConfigRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read module config %q: %w", path, err)
	}

	request := &decappb.UpdateConfigRequest{}
	if err := xproto.Unmarshal(data, request); err != nil {
		return nil, fmt.Errorf("failed to parse module config %q: %w", path, err)
	}

	switch request.GetName() {
	case "":
		request.Name = name
	case name:
	default:
		return nil, fmt.Errorf(
			"module config %q names config %q, but the function targets %q",
			path, request.GetName(), name,
		)
	}
	return request, nil
}

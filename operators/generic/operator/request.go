package operator

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xproto"
)

// LoadRequest reads a module config file, the method's request spelled in
// YAML, and decodes it against the descriptors linked into this binary.
func LoadRequest(method string, path string) (proto.Message, error) {
	request, err := operator.NewMethodRequest(method)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read module config %q: %w", path, err)
	}
	if err := xproto.Unmarshal(data, request); err != nil {
		return nil, fmt.Errorf("failed to parse module config %q: %w", path, err)
	}
	return request, nil
}

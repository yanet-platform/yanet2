package operator

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xproto"
)

// LoadRequest reads a module config file, the method's request spelled in
// YAML, and decodes it against the descriptor set, or against the
// descriptors linked into this binary when files is nil.
func LoadRequest(method string, path string, files *protoregistry.Files) (proto.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read module config %q: %w", path, err)
	}
	return decodeRequest(method, path, data, files)
}

// decodeRequest decodes the bytes of one file against its method's schema.
func decodeRequest(method string, path string, data []byte, files *protoregistry.Files) (proto.Message, error) {
	request, err := operator.NewMethodRequest(method, files)
	if err != nil {
		return nil, err
	}
	if err := xproto.Unmarshal(data, request); err != nil {
		return nil, fmt.Errorf("failed to parse module config %q: %w", path, err)
	}
	return request, nil
}

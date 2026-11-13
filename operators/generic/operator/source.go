package operator

import (
	"crypto/sha256"
	"fmt"
	"os"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// fileSource keeps the last fully decoded snapshot and its file fingerprints.
//
// One worker reloads it; published messages are never mutated.
type fileSource struct {
	configs []ModuleConfig
	targets []operator.StaticTarget
	hashes  [][sha256.Size]byte
}

// newFileSource resolves schemas once and fixes the functions for this run.
func newFileSource(cfg *Config) (*fileSource, error) {
	targets := make([]operator.StaticTarget, 0, len(cfg.Configs)+len(cfg.Functions))
	for idx, config := range cfg.Configs {
		var files *protoregistry.Files
		if config.Descriptors != "" {
			loaded, err := operator.LoadDescriptorSet(config.Descriptors)
			if err != nil {
				return nil, fmt.Errorf("configs[%d]: %w", idx, err)
			}
			files = loaded
		}
		targets = append(targets, operator.StaticTarget{
			Name:        config.Name.Unwrap(),
			Method:      config.Method.Unwrap(),
			Descriptors: files,
		})
	}
	for _, function := range cfg.Functions {
		targets = append(targets, operator.StaticTarget{
			Name:        function.Name.Unwrap(),
			Function:    function.AsFunction(),
			IgnorePdump: function.IgnorePdump,
		})
	}
	return &fileSource{
		configs: slices.Clone(cfg.Configs),
		targets: targets,
		hashes:  make([][sha256.Size]byte, len(cfg.Configs)),
	}, nil
}

// Reload returns a new snapshot only when the decoded requests change.
//
// Every file is read, so replacements with unchanged metadata are detected.
// A read, decode or name-binding failure leaves the entire snapshot intact.
func (m *fileSource) Reload() ([]operator.StaticTarget, error) {
	targets := slices.Clone(m.targets)
	hashes := make([][sha256.Size]byte, len(m.configs))
	changed := false
	for idx, config := range m.configs {
		path := config.File.Unwrap()
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("configs[%d]: failed to read module config %q: %w", idx, path, err)
		}
		hashes[idx] = sha256.Sum256(data)
		target := targets[idx]
		if target.Request != nil && hashes[idx] == m.hashes[idx] {
			continue
		}
		request, err := decodeRequest(target.Method, path, data, target.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		if err := bindRequestName(request, config); err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		if !proto.Equal(target.Request, request) {
			targets[idx].Request = request
			changed = true
		}
	}
	m.hashes = hashes
	if !changed {
		return nil, nil
	}
	m.targets = targets
	return targets, nil
}

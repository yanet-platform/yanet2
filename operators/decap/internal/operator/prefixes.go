package operator

import (
	"fmt"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// yamlPrefixFile is the top-level YAML structure for a decap prefixes file.
type yamlPrefixFile struct {
	Prefixes []string `yaml:"prefixes"`
}

// LoadDecapPrefixes reads a YAML file of the shape:
//
//	prefixes:
//	  - 10.0.0.0/8
//	  - 2000::/3
//
// Each entry is parsed as a CIDR prefix and masked, failing fast on
// malformed input.
func LoadDecapPrefixes(path string) ([]*commonpb.IPPrefix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read prefixes file %q: %w", path, err)
	}

	var file yamlPrefixFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse prefixes file %q: %w", path, err)
	}

	out := make([]*commonpb.IPPrefix, 0, len(file.Prefixes))
	for idx, s := range file.Prefixes {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		network, err := commonpb.NewIPPrefixFromPrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		out = append(out, network)
	}
	return out, nil
}

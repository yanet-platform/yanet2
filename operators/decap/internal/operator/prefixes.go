package operator

import (
	"fmt"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"
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
// Each entry is parsed with netip.ParsePrefix to fail fast on malformed
// input. The returned slice contains canonical Masked().String() forms.
func LoadDecapPrefixes(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read prefixes file %q: %w", path, err)
	}

	var file yamlPrefixFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse prefixes file %q: %w", path, err)
	}

	out := make([]string, 0, len(file.Prefixes))
	for idx, s := range file.Prefixes {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: failed to parse prefix %q: %w", idx, s, err)
		}
		out = append(out, prefix.Masked().String())
	}
	return out, nil
}

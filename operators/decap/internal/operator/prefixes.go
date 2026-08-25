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
	// Prefixes4 is the IPv4 CIDR list.
	Prefixes4 []string `yaml:"prefixes4"`
	// Prefixes6 is the IPv6 CIDR list.
	Prefixes6 []string `yaml:"prefixes6"`
}

// LoadDecapPrefixes reads a YAML file with family-typed prefixes4 and
// prefixes6 CIDR lists, mirroring the wire schema.
//
// Each entry is parsed as a CIDR prefix of its list's family and masked,
// failing fast on malformed or wrong-family input. An IPv4-mapped IPv6
// prefix belongs to prefixes6.
func LoadDecapPrefixes(path string) ([]*commonpb.IPv4Prefix, []*commonpb.IPv6Prefix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read prefixes file %q: %w", path, err)
	}

	var file yamlPrefixFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("failed to parse prefixes file %q: %w", path, err)
	}

	v4, err := parsePrefixes(file.Prefixes4, "prefixes4", commonpb.NewIPv4PrefixFromPrefix)
	if err != nil {
		return nil, nil, err
	}
	v6, err := parsePrefixes(file.Prefixes6, "prefixes6", commonpb.NewIPv6PrefixFromPrefix)
	if err != nil {
		return nil, nil, err
	}
	return v4, v6, nil
}

// parsePrefixes decodes one family-typed CIDR list through the family's
// message constructor, which rejects a wrong-family entry.
func parsePrefixes[T any](cidrs []string, key string, newPrefix func(netip.Prefix) (T, error)) ([]T, error) {
	out := make([]T, 0, len(cidrs))
	for idx, s := range cidrs {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, idx, err)
		}
		network, err := newPrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, idx, err)
		}
		out = append(out, network)
	}
	return out, nil
}

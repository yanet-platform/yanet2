package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
	"github.com/yanet-platform/yanet2/operators/forward/internal/operator"
)

// TestLoadForwardRules_Mode verifies that LoadForwardRules accepts both the
// canonical uppercase mode spellings and the legacy PascalCase spellings,
// mapping each to the same forwardpb.ForwardMode value, and rejects any
// other spelling.
func TestLoadForwardRules_Mode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		want    forwardpb.ForwardMode
		wantErr bool
	}{
		{name: "canonical NONE", mode: "NONE", want: forwardpb.ForwardMode_NONE},
		{name: "canonical IN", mode: "IN", want: forwardpb.ForwardMode_IN},
		{name: "canonical OUT", mode: "OUT", want: forwardpb.ForwardMode_OUT},
		{name: "legacy None", mode: "None", want: forwardpb.ForwardMode_NONE},
		{name: "legacy In", mode: "In", want: forwardpb.ForwardMode_IN},
		{name: "legacy Out", mode: "Out", want: forwardpb.ForwardMode_OUT},
		{name: "unknown spelling", mode: "out", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.yaml")
			data := "rules:\n  - target: fn:test\n    mode: " + tt.mode + "\n    counter: cnt\n"
			require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

			rules, err := operator.LoadForwardRules(path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, rules, 1)
			require.Equal(t, tt.want, rules[0].Action.Mode)
		})
	}
}

// TestLoadForwardRules_NetworkFamilySplit verifies that mixed srcs/dsts
// CIDR lists split into the family-typed proto lists, order preserved.
func TestLoadForwardRules_NetworkFamilySplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	data := `rules:
  - target: fn:test
    mode: NONE
    counter: cnt
    srcs:
      - 2001:db8::/32
      - 192.0.2.0/24
      - 10.0.0.0/8
      - 2001:db8:1::/48
    dsts:
      - 203.0.113.0/24
`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	rules, err := operator.LoadForwardRules(path)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	rule := rules[0]
	require.Equal(t, []string{"192.0.2.0/24", "10.0.0.0/8"}, networks4ToStrings(t, rule.Sources4))
	require.Equal(t, []string{"2001:db8::/32", "2001:db8:1::/48"}, networks6ToStrings(t, rule.Sources6))
	require.Equal(t, []string{"203.0.113.0/24"}, networks4ToStrings(t, rule.Destinations4))
	require.Empty(t, rule.Destinations6)
}

// TestLoadForwardRules_RejectsMalformedCIDR verifies that a rule file
// carrying an unparseable network fails to load instead of dropping it.
func TestLoadForwardRules_RejectsMalformedCIDR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	data := "rules:\n  - target: fn:test\n    mode: NONE\n    counter: cnt\n    srcs:\n      - 192.0.2.0/33\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	_, err := operator.LoadForwardRules(path)
	require.Error(t, err)
}

// networks4ToStrings renders IPv4Network messages in their canonical text
// form for comparison.
func networks4ToStrings(t *testing.T, nets []*commonpb.IPv4Network) []string {
	t.Helper()

	out := make([]string, len(nets))
	for idx, net := range nets {
		parsed, err := net.ToNetwork4()
		require.NoError(t, err)
		out[idx] = parsed.String()
	}
	return out
}

// networks6ToStrings is networks4ToStrings for IPv6Network messages.
func networks6ToStrings(t *testing.T, nets []*commonpb.IPv6Network) []string {
	t.Helper()

	out := make([]string, len(nets))
	for idx, net := range nets {
		parsed, err := net.ToNetwork6()
		require.NoError(t, err)
		out[idx] = parsed.String()
	}
	return out
}

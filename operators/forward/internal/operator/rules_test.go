package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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

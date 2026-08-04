package lib

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewErrorStepAndNewSkipStep_GenerateValidGo verifies that NewErrorStep
// and NewSkipStep always emit compilable Go, and that the reason text
// survives intact, even when reason itself contains characters that are
// hazardous when embedded in generated source: quotes, backslashes,
// newlines and a literal '%' that fmt.Fatalf/Skipf could otherwise
// misinterpret as a format verb.
func TestNewErrorStepAndNewSkipStep_GenerateValidGo(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{name: "plain text", reason: "invalid format"},
		{name: "double quotes", reason: `FIB entry "not-a-cidr": no '/'`},
		{name: "backslash", reason: `path C:\config\route0.yaml missing`},
		{name: "percent verb", reason: "100% of routes failed: %s %d %v"},
		{name: "newline", reason: "line one\nline two"},
		{name: "mixed", reason: `bad "value" at C:\x, 50% done` + "\n" + `next line with "quotes"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStep := NewErrorStep("stepType", tt.reason)
			requireGoCodeParses(t, errStep.GoCode)
			require.Contains(t, errStep.GoCode, "t.Fatalf")
			require.True(t, strings.Contains(errStep.GoCode, "Step failed: "),
				"generated code must carry the reason prefix:\n%s", errStep.GoCode)

			skipStep := NewSkipStep("stepType", tt.reason)
			requireGoCodeParses(t, skipStep.GoCode)
			require.Contains(t, skipStep.GoCode, "t.Skipf")
			require.True(t, strings.Contains(skipStep.GoCode, "Step skipped: "),
				"generated code must carry the reason prefix:\n%s", skipStep.GoCode)
		})
	}
}

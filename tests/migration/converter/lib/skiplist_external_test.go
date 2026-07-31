package lib_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/migration/converter/lib"
)

// TestUpdateSkiplistOmitsPureSeparatorComments verifies standalone separator comments are omitted.
func TestUpdateSkiplistOmitsPureSeparatorComments(t *testing.T) {
	testCases := []struct {
		name          string
		originalLines string
		omittedLine   string
		preservedLine string
	}{
		{
			name:          "standalone separator",
			originalLines: "\n        ====================\n",
			omittedLine:   "#         ====================",
		},
		{
			name:          "setext underline",
			originalLines: "        Labeled section\n        --------------------\n",
			preservedLine: "#         --------------------",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			inputDirectory := filepath.Join(root, "input")
			autotestPath := filepath.Join(inputDirectory, "001_one_port", "example", "autotest.yaml")
			require.NoError(t, os.MkdirAll(filepath.Dir(autotestPath), 0o755))
			input := "steps:\n  - cli:\n      command: |\n" + testCase.originalLines
			require.NoError(t, os.WriteFile(autotestPath, []byte(input), 0o644))

			skiplistPath := filepath.Join(root, "skiplist.yaml")
			marker := "# ---- Auto-generated entries below (do not edit) ----\n"
			require.NoError(t, os.WriteFile(skiplistPath, []byte(marker), 0o644))

			converter, err := lib.NewConverter(&lib.Config{InputDir: inputDirectory, SkiplistPath: skiplistPath})
			require.NoError(t, err)
			require.NoError(t, converter.UpdateSkiplist())

			content, err := os.ReadFile(skiplistPath)
			require.NoError(t, err)
			generated := string(content)
			require.Contains(t, generated, marker)
			if testCase.omittedLine != "" {
				require.NotContains(t, generated, testCase.omittedLine)
			}
			if testCase.preservedLine != "" {
				require.Contains(t, generated, testCase.preservedLine)
			}
		})
	}
}

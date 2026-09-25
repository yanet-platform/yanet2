package framework_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Test_Framework_Run_DependencyPolicy verifies that real child test outcomes
// preserve failure, skip dependencies, and leave independent parents runnable.
func Test_Framework_Run_DependencyPolicy(t *testing.T) {
	for _, tc := range []string{"pass", "skip", "fatal", "error", "cleanup", "nested", "parent"} {
		t.Run(tc, func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "tool", "test2json", "-t", executable,
				"-test.run=^Test_Framework_Run_Process$", "-test.v=test2json", "-test.timeout=45s",
			)
			command.Env = append(os.Environ(), "FRAMEWORK_RUN_CASE="+tc)
			output, runErr := command.CombinedOutput()
			require.NoError(t, ctx.Err(), "%s", output)
			failed := tc != "pass" && tc != "skip"
			if failed {
				require.Error(t, runErr, "%s", output)
			} else {
				require.NoError(t, runErr, "%s", output)
			}
			actions := map[string]string{}
			var logs strings.Builder
			decoder := json.NewDecoder(bytes.NewReader(output))
			for {
				var event struct{ Action, Test, Output string }
				err := decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				require.NoError(t, err, "%s", output)
				if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
					actions[event.Test] = event.Action
				}
				logs.WriteString(event.Output)
			}
			prefix := "Test_Framework_Run_Process/sequence"
			if failed {
				require.Equal(t, "fail", actions[prefix])
				require.Equal(t, "skip", actions[prefix+"/later"])
				require.Equal(t, "fail", actions[prefix+"/snapshot"])
				require.Contains(t, logs.String(), "failed to restore snapshot")
				require.NotContains(t, logs.String(), "LATER_BODY")
				require.Contains(t, logs.String(), "RESET_DELTA=0")
				if tc != "parent" {
					require.Equal(t, "fail", actions[prefix+"/first"])
					require.Contains(t, logs.String(), "FIRST_RESULT=false")
				}
			} else {
				require.Equal(t, "pass", actions[prefix])
				require.Equal(t, "pass", actions[prefix+"/later"])
				require.Contains(t, logs.String(), "FIRST_RESULT=true")
				require.Contains(t, logs.String(), "RESET_DELTA=1")
			}
			if tc == "skip" {
				require.Equal(t, "skip", actions[prefix+"/first"])
			}
			if tc == "pass" {
				require.Equal(t, "pass", actions[prefix+"/first"])
			}
			if tc == "nested" {
				require.Equal(t, "fail", actions[prefix+"/first/nested"])
			}
			require.Equal(t, "pass", actions["Test_Framework_Run_Process/fresh/step"])
			require.Contains(t, logs.String(), "LATER_RESULT=true")
			require.Contains(t, logs.String(), "POSTLUDE")
			require.Contains(t, logs.String(), "PARENT_CLEANUP")
			require.NotContains(t, logs.String(), "SNAPSHOT_BODY")
			require.NotContains(t, logs.String(), "BAD_ORDER")
		})
	}
}

// Test_Framework_Run_Process verifies that sequential steps use their child's
// test context; intentional failures are observed by a bounded parent process.
func Test_Framework_Run_Process(t *testing.T) {
	scenario := os.Getenv("FRAMEWORK_RUN_CASE")
	if scenario == "" {
		return
	}
	core, logs := observer.New(zap.DebugLevel)
	owner, err := framework.New(&framework.Config{
		Name: "dependent-steps", ProjectRoot: t.TempDir(),
	}, framework.WithLog(zap.New(core).Sugar()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Global().Stop()) })
	t.Run("sequence", func(t *testing.T) {
		t.Cleanup(func() { t.Log("PARENT_CLEANUP") })
		bound := owner.ForTest(t)
		order := 0
		if scenario == "parent" {
			t.Error("intentional parent failure")
		} else {
			result := bound.Run("first", func(child *framework.TestFramework, t *testing.T) {
				order++
				switch scenario {
				case "fatal":
					t.Fatal("intentional fatal failure")
				case "error":
					t.Error("intentional nonfatal failure")
				case "cleanup":
					t.Cleanup(func() { t.Error("intentional cleanup failure") })
				case "nested":
					child.Run("nested", func(_ *framework.TestFramework, t *testing.T) {
						t.Error("intentional nested failure")
					})
				case "skip":
					t.Skip("ordinary skip")
				}
			})
			t.Logf("FIRST_RESULT=%t", result)
		}
		before := logs.FilterMessageSnippet("Resetting socket connections").Len()
		result := bound.Run("later", func(child *framework.TestFramework, t *testing.T) {
			t.Log("LATER_BODY")
			if order != 1 {
				t.Error("BAD_ORDER")
			}
			order++
		})
		t.Logf("RESET_DELTA=%d", logs.FilterMessageSnippet("Resetting socket connections").Len()-before)
		t.Logf("LATER_RESULT=%t", result)
		if t.Failed() {
			bound.RunWith("missing", "snapshot", func(_ *framework.TestFramework, t *testing.T) {
				t.Log("SNAPSHOT_BODY")
			})
		}
		t.Log("POSTLUDE")
	})
	t.Run("fresh", func(t *testing.T) {
		owner.ForTest(t).Run("step", func(_ *framework.TestFramework, t *testing.T) {})
	})
}

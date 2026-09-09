package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/builtin"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_Logging_GetLevel_ReportsStartupLevel verifies that the level read back
// is the one the service started with, for every level the API represents.
func Test_Logging_GetLevel_ReportsStartupLevel(t *testing.T) {
	cases := []struct {
		name         string
		zapLevel     zapcore.Level
		wantAPILevel ynpb.LogLevel
	}{
		{
			name:         "debug",
			zapLevel:     zapcore.DebugLevel,
			wantAPILevel: ynpb.LogLevel_DEBUG,
		},
		{
			name:         "info",
			zapLevel:     zapcore.InfoLevel,
			wantAPILevel: ynpb.LogLevel_INFO,
		},
		{
			name:         "warn",
			zapLevel:     zapcore.WarnLevel,
			wantAPILevel: ynpb.LogLevel_WARN,
		},
		{
			name:         "error",
			zapLevel:     zapcore.ErrorLevel,
			wantAPILevel: ynpb.LogLevel_ERROR,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			atom := zap.NewAtomicLevelAt(tc.zapLevel)
			svc := builtin.NewLogging(&atom)

			resp, err := svc.GetLevel(t.Context(), &ynpb.GetLevelRequest{})

			require.NoError(t, err)
			require.Equal(t, tc.wantAPILevel, resp.GetLevel())
		})
	}
}

// Test_Logging_UpdateLevel_GetLevel_RoundTrips verifies that a level just set is
// read back unchanged and reaches the observer exactly once.
func Test_Logging_UpdateLevel_GetLevel_RoundTrips(t *testing.T) {
	cases := []struct {
		name  string
		level ynpb.LogLevel
	}{
		{
			name:  "debug",
			level: ynpb.LogLevel_DEBUG,
		},
		{
			name:  "info",
			level: ynpb.LogLevel_INFO,
		},
		{
			name:  "warn",
			level: ynpb.LogLevel_WARN,
		},
		{
			name:  "error",
			level: ynpb.LogLevel_ERROR,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			atom := zap.NewAtomicLevelAt(zapcore.InfoLevel)

			var observed zapcore.Level
			var observedCount int
			svc := builtin.NewLogging(&atom, builtin.WithLoggingLevelObserver(func(level zapcore.Level) {
				observed = level
				observedCount++
			}))

			_, err := svc.UpdateLevel(t.Context(), &ynpb.UpdateLevelRequest{Level: tc.level})
			require.NoError(t, err)

			resp, err := svc.GetLevel(t.Context(), &ynpb.GetLevelRequest{})
			require.NoError(t, err)

			require.Equal(t, tc.level, resp.GetLevel())
			require.Equal(t, 1, observedCount)
			require.Equal(t, atom.Level(), observed)
		})
	}
}

// Test_Logging_GetLevel_NilAtomicLevel verifies that reading the level without a
// dynamic level in place answers Unimplemented.
func Test_Logging_GetLevel_NilAtomicLevel(t *testing.T) {
	svc := builtin.NewLogging(nil)

	_, err := svc.GetLevel(t.Context(), &ynpb.GetLevelRequest{})

	require.Equal(t, codes.Unimplemented, status.Code(err))
}

// Test_Logging_GetLevel_UnrepresentableLevel verifies that a startup level the
// API cannot represent answers FailedPrecondition.
func Test_Logging_GetLevel_UnrepresentableLevel(t *testing.T) {
	atom := zap.NewAtomicLevelAt(zapcore.FatalLevel)
	svc := builtin.NewLogging(&atom)

	_, err := svc.GetLevel(t.Context(), &ynpb.GetLevelRequest{})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

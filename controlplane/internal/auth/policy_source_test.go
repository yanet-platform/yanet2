package auth_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/testpolicy"
)

const (
	policyTestMethod = "/test.Service/Method"
	emptyGrants      = "permissions: {users: []}\n"
)

// managedAuthConfig replaces file-based RBAC with a scoped policy store.
func managedAuthConfig(t *testing.T) auth.Config {
	t.Helper()
	config := buildEnabledConfig(t)
	config.IdentityProviders = nil
	config.PermissionsPath = ""
	config.ManagedPolicy = testpolicy.Config(t.TempDir())
	return config
}

// aliceGeneration grants one method to the configured basic account.
func aliceGeneration(sequence uint64) testpolicy.Generation {
	return testpolicy.ForAccount(sequence, "alice", policyTestMethod)
}

// policyMetric returns a gauge or counter from the live manager.
func policyMetric(t *testing.T, manager *auth.Manager, name string) *commonpb.Metric {
	t.Helper()
	metric := testpolicy.Metric(manager.Collect(), name)
	require.NotNil(t, metric, "missing policy metric %s", name)
	return metric
}

// Test_ManagedPolicy_RejectsInvalidUpdates verifies that a rejected
// publication keeps the last good grants and does not consume a revision.
func Test_ManagedPolicy_RejectsInvalidUpdates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*testpolicy.Generation)
	}{
		{"older revision", func(next *testpolicy.Generation) { next.Sequence = 1 }},
		{"conflicting revision", func(next *testpolicy.Generation) { next.Sequence = 2 }},
		{"foreign installation", func(next *testpolicy.Generation) { next.Scope.Epoch = "other" }},
		{"foreign Node", func(next *testpolicy.Generation) { next.Scope.NodeUID = "other" }},
		{"foreign server", func(next *testpolicy.Generation) { next.Scope.ServerURI += "-other" }},
		{"valid manifest under wrong digest", func(next *testpolicy.Generation) { next.WrongGenerationName = true }},
		{"valid grants with different bytes", func(next *testpolicy.Generation) { next.ChangeFileBytes = "permissions.yaml" }},
		{"missing identities", func(next *testpolicy.Generation) { next.OmitFile = "identities.yaml" }},
		{"missing grants", func(next *testpolicy.Generation) { next.OmitFile = "permissions.yaml" }},
		{"implicit empty", func(next *testpolicy.Generation) { next.Permissions = "permissions: {}\n" }},
		{"ambiguous identities", func(next *testpolicy.Generation) {
			next.Identities = "identities: [{username: alice}, {username: alice}]\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := managedAuthConfig(t)
			directory := config.ManagedPolicy.Directory
			aliceGeneration(2).Publish(t, directory)
			manager := testpolicy.Start(t, &config)

			next := aliceGeneration(3)
			next.Permissions = emptyGrants
			tc.change(&next)
			next.Publish(t, directory)
			require.Eventually(t, func() bool {
				return testpolicy.Metric(manager.Collect(), "auth_policy_reload_errors_total").GetCounter() > 0
			}, time.Second, 5*time.Millisecond, "invalid publication was not rejected")
			require.EqualValues(t, 2, policyMetric(t, manager, "auth_policy_active_revision").GetGauge())
			credential := core.Credential{Token: basicToken("alice", "s3cret")}
			_, _, release, err := manager.Admit(t.Context(), credential, policyTestMethod)
			require.NoError(t, err)
			release()

			// A valid generation must still activate after a rejection.
			testpolicy.ForAccount(3, "alice").Publish(t, directory)
			require.Eventually(t, func() bool {
				return testpolicy.Metric(manager.Collect(), "auth_policy_active_revision").GetGauge() == 3
			}, time.Second, 5*time.Millisecond, "corrected revision did not activate")
			_, _, _, err = manager.Admit(t.Context(), credential, policyTestMethod)
			require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
		})
	}
}

// Test_ManagedPolicy_ColdStartUsesOnlyCurrent verifies that restart waits for
// an in-flight writer and does not fall back to old grants.
func Test_ManagedPolicy_ColdStartUsesOnlyCurrent(t *testing.T) {
	t.Parallel()
	config := managedAuthConfig(t)
	_, err := auth.NewManager(&config)
	require.ErrorContains(t, err, "load initial gateway policy")

	directory := config.ManagedPolicy.Directory
	aliceGeneration(1).Publish(t, directory)
	empty := testpolicy.Generation{
		Scope: testpolicy.Scope(), Sequence: 2,
		Identities: "identities: []\n", Permissions: emptyGrants,
	}
	empty.Publish(t, directory)
	manager := restartAfterPublication(t, &config)
	require.EqualValues(t, 2, policyMetric(t, manager, "auth_policy_active_revision").GetGauge())
	_, _, _, err = manager.Admit(t.Context(), core.Credential{
		Token: basicToken("alice", "s3cret"),
	}, policyTestMethod)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	_, _, _, err = manager.Admit(t.Context(), core.Credential{}, policyTestMethod)
	require.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)

	empty.Sequence++
	empty.OmitFile = "permissions.yaml"
	empty.Publish(t, directory)
	_, err = auth.NewManager(&config)
	require.ErrorContains(t, err, "permissions.yaml")
}

// restartAfterPublication releases the writer lock after the reader has had
// a chance to try, without switching the current generation.
func restartAfterPublication(t *testing.T, config *auth.Config) *auth.Manager {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(config.ManagedPolicy.Directory, ".publish-lock"), os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	require.NoError(t, unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	var manager *auth.Manager
	ready := make(chan error, 1)
	go func() {
		var err error
		manager, err = auth.NewManager(config)
		ready <- err
	}()
	select {
	case err := <-ready:
		if manager != nil {
			manager.Close()
		}
		t.Fatalf("startup completed while the writer held the lock: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	require.NoError(t, unix.Flock(int(lock.Fd()), unix.LOCK_UN))
	require.NoError(t, <-ready)
	t.Cleanup(manager.Close)
	return manager
}

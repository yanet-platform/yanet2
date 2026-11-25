package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/testpolicy"
)

// admittedCall runs a unary call carrying the admission id through the
// interceptor and returns what its handler saw.
//
// That is the principal and the credential, or the refusal.
func admittedCall(ctx context.Context, manager *auth.Manager, method string) (*core.Principal, core.Credential, error) {
	var principal *core.Principal
	var credential core.Credential
	_, err := manager.UnaryServerInterceptor()(
		ctx, "request", &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, request any) (any, error) {
			principal = core.FromContext(ctx)
			credential = auth.ExtractCredential(ctx)
			return nil, nil
		},
	)

	return principal, credential, err
}

// admissionContext represents an HTTP call handed to the loopback server.
func admissionContext(ctx context.Context, id string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(auth.AdmissionMetadataKey, id))
}

// Test_Manager_Admit_ServerTakesAdmissionOnce verifies that a call carrying
// an admission's id runs as its principal, and the id lets no second call in.
//
// The call also sees the admitted credential.
func Test_Manager_Admit_ServerTakesAdmissionOnce(t *testing.T) {
	config := buildEnabledConfig(t)
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)
	method := "/test.Service/Method"
	credential := core.Credential{Token: basicToken("alice", "s3cret")}

	principal, id, release, err := manager.Admit(t.Context(), credential, method)
	require.NoError(t, err)
	t.Cleanup(release)
	require.Equal(t, "alice", principal.User)

	ctx := admissionContext(t.Context(), id)
	seen, seenCredential, err := admittedCall(ctx, manager, method)
	require.NoError(t, err)
	require.Equal(t, "alice", seen.User)
	require.Equal(t, credential.Token, seenCredential.Token)

	_, _, err = admittedCall(ctx, manager, method)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "second call: %v", err)
}

// Test_Manager_Admit_ServerChecksAdmission verifies that a call is refused
// for an unknown or released admission id.
//
// Even another method that the principal could call needs its own admission.
func Test_Manager_Admit_ServerChecksAdmission(t *testing.T) {
	config := buildEnabledConfig(t)
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)
	credential := core.Credential{Token: basicToken("alice", "s3cret")}

	const otherMethod = "/test.Service/OtherMethod"
	_, released, release, err := manager.Admit(t.Context(), credential, otherMethod)
	require.NoError(t, err)
	release()
	_, admitted, releaseAdmitted, err := manager.Admit(t.Context(), credential, "/test.Service/Method")
	require.NoError(t, err)
	t.Cleanup(releaseAdmitted)

	for _, tc := range []struct {
		name   string
		id     string
		method string
		code   codes.Code
	}{
		{name: "unknown admission", id: "unknown", method: "/test.Service/Method", code: codes.Unauthenticated},
		{name: "released admission", id: released, method: otherMethod, code: codes.Unauthenticated},
		{name: "different allowed method", id: admitted, method: otherMethod, code: codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := admittedCall(admissionContext(t.Context(), tc.id), manager, tc.method)
			require.Equal(t, tc.code, status.Code(err), "refusal: %v", err)
		})
	}
}

// Test_Manager_Admit_RefusesCall verifies that Admit refuses a credential
// that fails verification and a method the principal may not call.
//
// The first gets Unauthenticated, the second PermissionDenied.
func Test_Manager_Admit_RefusesCall(t *testing.T) {
	config := buildEnabledConfig(t)
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		token  string
		method string
		code   codes.Code
	}{
		{name: "credential that fails verification", token: basicToken("alice", "wrong"), method: "/test.Service/Method", code: codes.Unauthenticated},
		{name: "method the principal may not call", token: basicToken("alice", "s3cret"), method: "/other.Service/Method", code: codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := manager.Admit(t.Context(), core.Credential{Token: tc.token}, tc.method)
			require.Equal(t, tc.code, status.Code(err), "refusal: %v", err)
		})
	}
}

// admissionLookupContext runs a fixture's revision switch at metadata lookup,
// after interceptor entry but before the one-shot admission is consumed.
type admissionLookupContext struct {
	context.Context
	BeforeLookup func()
}

func (m *admissionLookupContext) Value(key any) any {
	if before := m.BeforeLookup; before != nil {
		m.BeforeLookup = nil
		before()
	}
	return m.Context.Value(key)
}

// Test_ManagedPolicy_RevisionFencesAdmissions verifies that a revision switch
// rejects stale admissions while an admitted unary keeps its original policy.
func Test_ManagedPolicy_RevisionFencesAdmissions(t *testing.T) {
	t.Parallel()
	config := managedAuthConfig(t)
	directory := config.ManagedPolicy.Directory
	aliceGeneration(1).Publish(t, directory)
	manager := testpolicy.Start(t, &config)
	credential := core.Credential{Token: basicToken("alice", "s3cret")}
	park := func() context.Context {
		_, id, release, err := manager.Admit(t.Context(), credential, policyTestMethod)
		require.NoError(t, err)
		t.Cleanup(release)
		return admissionContext(t.Context(), id)
	}
	racing := &admissionLookupContext{Context: park(), BeforeLookup: func() {
		aliceGeneration(2).Publish(t, directory)
		require.Eventually(t, func() bool {
			return testpolicy.Metric(manager.Collect(), "auth_policy_active_revision").GetGauge() == 2
		}, time.Second, 5*time.Millisecond)
	}}
	for _, ctx := range []context.Context{racing, park()} {
		principal, _, err := admittedCall(ctx, manager, policyTestMethod)
		require.Nil(t, principal, "a stale HTTP admission reached the handler")
		require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
		_, _, err = admittedCall(ctx, manager, policyTestMethod)
		require.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	}

	_, err := manager.UnaryServerInterceptor()(
		park(), nil, &grpc.UnaryServerInfo{FullMethod: policyTestMethod},
		func(ctx context.Context, request any) (any, error) {
			next := aliceGeneration(3)
			next.Identities = "identities: []\n"
			next.Permissions = emptyGrants
			next.Publish(t, directory)
			require.Eventually(t, func() bool {
				return testpolicy.Metric(manager.Collect(), "auth_policy_active_revision").GetGauge() == 3
			}, time.Second, 5*time.Millisecond)
			principal, err := manager.Authenticate(ctx, credential, nil)
			require.NoError(t, err, "admitted unary lost its identity snapshot")
			require.Equal(t, "alice", principal.User)
			return nil, manager.Authorize(ctx, principal, policyTestMethod)
		},
	)
	require.NoError(t, err, "admitted unary lost its permission snapshot")
	_, _, _, err = manager.Admit(t.Context(), credential, policyTestMethod)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "new call used the old identity")
}

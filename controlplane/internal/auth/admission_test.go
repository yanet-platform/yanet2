package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// admittedCall runs a unary call carrying the admission id through the
// interceptor and returns what its handler saw.
//
// That is the principal and the credential, or the refusal.
func admittedCall(t *testing.T, manager *auth.Manager, id string, method string) (*core.Principal, core.Credential, error) {
	t.Helper()

	var principal *core.Principal
	var credential core.Credential
	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(auth.AdmissionMetadataKey, id))
	_, err := auth.UnaryServerInterceptor(manager, zap.NewNop())(
		ctx, "request", &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, request any) (any, error) {
			principal = core.FromContext(ctx)
			credential = auth.ExtractCredential(ctx)
			return nil, nil
		},
	)

	return principal, credential, err
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

	seen, seenCredential, err := admittedCall(t, manager, id, method)
	require.NoError(t, err)
	require.Equal(t, "alice", seen.User)
	require.Equal(t, credential.Token, seenCredential.Token)

	_, _, err = admittedCall(t, manager, id, method)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "second call: %v", err)
}

// Test_Manager_Admit_ServerChecksAdmission verifies that a call is refused
// for an unknown or released admission id.
//
// It is also refused when the admitted principal may not call the method
// the call reached.
func Test_Manager_Admit_ServerChecksAdmission(t *testing.T) {
	config := buildEnabledConfig(t)
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)
	credential := core.Credential{Token: basicToken("alice", "s3cret")}

	_, released, release, err := manager.Admit(t.Context(), credential, "/test.Service/Method")
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
		{name: "released admission", id: released, method: "/test.Service/Method", code: codes.Unauthenticated},
		{name: "method the principal may not call", id: admitted, method: "/other.Service/Method", code: codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := admittedCall(t, manager, tc.id, tc.method)
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

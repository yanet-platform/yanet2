package auth_test

import (
	"context"
	"os"
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

func TestUnaryServerInterceptor(t *testing.T) {
	log := zap.NewNop()

	tests := []struct {
		name           string
		disabled       bool
		withToken      bool
		token          string
		checkPrincipal func(*testing.T, *core.Principal)
	}{
		{
			name:      "disabled mode without token",
			disabled:  true,
			withToken: false,
			checkPrincipal: func(t *testing.T, p *core.Principal) {
				if p == nil {
					t.Fatal("Principal is nil")
				}
				if p.User != "anonymous" {
					t.Errorf("User = %q, want %q", p.User, "anonymous")
				}
				if !p.IsAnonymous {
					t.Error("IsAnonymous = false, want true")
				}
			},
		},
		{
			name:      "disabled mode with token",
			disabled:  true,
			withToken: true,
			token:     "some-token",
			checkPrincipal: func(t *testing.T, p *core.Principal) {
				if p == nil {
					t.Fatal("Principal is nil")
				}
				if p.User != "anonymous" {
					t.Errorf("User = %q, want %q", p.User, "anonymous")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := auth.NewManager(&auth.Config{
				Mode: auth.ModeDisabled,
			})
			require.NoError(t, err)

			interceptor := auth.UnaryServerInterceptor(manager, log)

			// Create context with or without token.
			ctx := context.Background()
			if tt.withToken {
				md := metadata.Pairs("x-yanet-authentication", tt.token)
				ctx = metadata.NewIncomingContext(ctx, md)
			}

			// Create a mock handler that extracts and verifies the Principal.
			var capturedPrincipal *core.Principal
			handler := func(ctx context.Context, req any) (any, error) {
				capturedPrincipal = core.FromContext(ctx)
				return "response", nil
			}

			// Call the interceptor.
			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/Method",
			}
			_, err = interceptor(ctx, "request", info, handler)
			if err != nil {
				t.Fatalf("interceptor() error = %v", err)
			}

			// Check the captured Principal.
			if tt.checkPrincipal != nil {
				tt.checkPrincipal(t, capturedPrincipal)
			}
		})
	}
}

func TestStreamServerInterceptor(t *testing.T) {
	log := zap.NewNop()
	manager, err := auth.NewManager(&auth.Config{
		Mode: auth.ModeDisabled,
	})
	require.NoError(t, err)

	interceptor := auth.StreamServerInterceptor(manager, log)

	// Create context without token.
	ctx := context.Background()

	// Create a mock server stream.
	mockStream := &mockServerStream{ctx: ctx}

	// Create a mock handler that extracts the Principal.
	var capturedPrincipal *core.Principal
	handler := func(srv any, stream grpc.ServerStream) error {
		capturedPrincipal = core.FromContext(stream.Context())
		return nil
	}

	// Call the interceptor.
	info := &grpc.StreamServerInfo{
		FullMethod: "/test.Service/StreamMethod",
	}
	err = interceptor(nil, mockStream, info, handler)
	require.NoError(t, err)

	// Check the captured Principal.
	if capturedPrincipal == nil {
		t.Fatal("Principal is nil")
	}
	if capturedPrincipal.User != "anonymous" {
		t.Errorf("User = %q, want %q", capturedPrincipal.User, "anonymous")
	}
}

func TestExtractCredential_Token(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{
			name: "with token",
			md:   metadata.Pairs("x-yanet-authentication", "test-token"),
			want: "test-token",
		},
		{
			name: "without token",
			md:   metadata.MD{},
			want: "",
		},
		{
			name: "multiple values (first wins)",
			md:   metadata.Pairs("x-yanet-authentication", "token1", "x-yanet-authentication", "token2"),
			want: "token1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(t.Context(), tt.md)
			got := auth.ExtractCredential(ctx).Token
			if got != tt.want {
				t.Errorf("ExtractCredential().Token = %q, want %q", got, tt.want)
			}
		})
	}

	// Test without metadata in context.
	t.Run("no metadata", func(t *testing.T) {
		ctx := t.Context()
		got := auth.ExtractCredential(ctx).Token
		if got != "" {
			t.Errorf("ExtractCredential().Token = %q, want empty", got)
		}
	})
}

// mockServerStream is a mock implementation of grpc.ServerStream for testing.
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

// Test_AuthenticationInterceptors_Audit_PreservesOutcomes verifies that invalid
// credentials become anonymous without changing handler responses or status.
func Test_AuthenticationInterceptors_Audit_PreservesOutcomes(t *testing.T) {
	config := buildEnabledConfig(t)
	config.Mode = auth.ModeAudit
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		token     string
		user      string
		anonymous bool
	}{
		{name: "missing credentials are anonymous", user: "anonymous", anonymous: true},
		{name: "unsupported credentials are anonymous", token: "unknown secret", user: "anonymous", anonymous: true},
		{name: "malformed credentials are anonymous", token: "Basic !!!secret", user: "anonymous", anonymous: true},
		{name: "unverified account name stays anonymous", token: basicToken("alice", "wrong"), user: "anonymous", anonymous: true},
		{name: "verified subject skips account lookup", token: basicToken("ghost", "s3cret"), user: "ghost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-yanet-authentication", tc.token))
			handlerError := status.Error(codes.Aborted, "handler outcome")
			unary := auth.UnaryServerInterceptor(manager, zap.NewNop())
			response, err := unary(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, func(ctx context.Context, request any) (any, error) {
				principal := core.FromContext(ctx)
				require.Equal(t, tc.user, principal.User)
				require.Equal(t, tc.anonymous, principal.IsAnonymous)
				return "response", handlerError
			})
			require.Equal(t, "response", response)
			require.Same(t, handlerError, err)

			stream := auth.StreamServerInterceptor(manager, zap.NewNop())
			err = stream(nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/test.Service/Method"}, func(server any, stream grpc.ServerStream) error {
				principal := core.FromContext(stream.Context())
				require.Equal(t, tc.user, principal.User)
				require.Equal(t, tc.anonymous, principal.IsAnonymous)
				return handlerError
			})
			require.Same(t, handlerError, err)
		})
	}
}

// Test_AuthenticationInterceptors_Enabled_RejectsInvalidCredentials verifies
// that failed verification rejects RPCs even when anonymous callers have access.
func Test_AuthenticationInterceptors_Enabled_RejectsInvalidCredentials(t *testing.T) {
	config := buildEnabledConfig(t)
	permissions := "permissions:\n  users:\n    - username: anonymous\n      permissions: [\"/test.Service/Method\"]\n"
	require.NoError(t, os.WriteFile(config.PermissionsPath, []byte(permissions), 0o600))
	manager, err := auth.NewManager(&config)
	require.NoError(t, err)
	method := "/test.Service/Method"
	anonymous, err := manager.Authenticate(t.Context(), core.Credential{}, &core.RequestInfo{FullMethod: method})
	require.NoError(t, err)
	require.NoError(t, manager.Authorize(t.Context(), anonymous, method))

	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-yanet-authentication", basicToken("alice", "wrong")))
	_, err = auth.UnaryServerInterceptor(manager, zap.NewNop())(
		ctx, "request", &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, request any) (any, error) {
			t.Fatal("unary handler ran after failed credential verification")
			return nil, nil
		},
	)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	err = auth.StreamServerInterceptor(manager, zap.NewNop())(
		nil, &mockServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: method},
		func(server any, stream grpc.ServerStream) error {
			t.Fatal("stream handler ran after failed credential verification")
			return nil
		},
	)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

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
			manager, err := NewManager(&Config{
				Disabled: tt.disabled,
			})
			require.NoError(t, err)

			interceptor := UnaryServerInterceptor(manager, log)

			// Create context with or without token.
			ctx := context.Background()
			if tt.withToken {
				md := metadata.Pairs(authMetadataKey, tt.token)
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
	manager, err := NewManager(&Config{
		Disabled: true,
	})
	require.NoError(t, err)

	interceptor := StreamServerInterceptor(manager, log)

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

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{
			name: "with token",
			md:   metadata.Pairs(authMetadataKey, "test-token"),
			want: "test-token",
		},
		{
			name: "without token",
			md:   metadata.MD{},
			want: "",
		},
		{
			name: "multiple values (first wins)",
			md:   metadata.Pairs(authMetadataKey, "token1", authMetadataKey, "token2"),
			want: "token1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tt.md)
			got := ExtractToken(ctx)
			if got != tt.want {
				t.Errorf("extractToken() = %q, want %q", got, tt.want)
			}
		})
	}

	// Test without metadata in context.
	t.Run("no metadata", func(t *testing.T) {
		ctx := context.Background()
		got := ExtractToken(ctx)
		if got != "" {
			t.Errorf("extractToken() = %q, want empty", got)
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

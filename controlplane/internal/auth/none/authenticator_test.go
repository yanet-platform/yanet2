package none

import (
	"context"
	"testing"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

func TestNoneAuthenticator_IsTokenSupported(t *testing.T) {
	auth := NewNoneAuthenticator()

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{
			name:  "empty token",
			token: "",
			want:  true,
		},
		{
			name:  "any token",
			token: "some-token",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.IsTokenSupported(tt.token); got != tt.want {
				t.Errorf("IsTokenSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNoneAuthenticator_Authenticate(t *testing.T) {
	auth := NewNoneAuthenticator()
	ctx := context.Background()

	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "empty token",
			token: "",
		},
		{
			name:  "with token",
			token: "some-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}
			principal, err := auth.Authenticate(ctx, tt.token, requestInfo)
			if err != nil {
				t.Fatalf("Authenticate() error = %v, want nil", err)
			}

			if principal == nil {
				t.Fatal("Authenticate() returned nil principal")
			}

			if principal.User != "anonymous" {
				t.Errorf("principal.User = %q, want %q", principal.User, "anonymous")
			}

			if len(principal.Groups) != 0 {
				t.Errorf("principal.Groups = %v, want empty", principal.Groups)
			}

			if principal.AuthMethod != "none" {
				t.Errorf("principal.AuthMethod = %q, want %q", principal.AuthMethod, "none")
			}

			if !principal.IsAnonymous {
				t.Error("principal.IsAnonymous = false, want true")
			}

			if principal.AuthTime.IsZero() {
				t.Error("principal.AuthTime is zero, want non-zero")
			}
		})
	}
}

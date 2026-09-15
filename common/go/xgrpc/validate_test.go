package xgrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// fakeValidatable is a message whose Validate() returns a fixed error,
// standing in for a generated request type in interceptor tests.
type fakeValidatable struct {
	err error
}

func (m *fakeValidatable) Validate() error { return m.err }

// fakeServerStream is a minimal grpc.ServerStream stub, enough to drive
// ValidateStreamInterceptor's RecvMsg wrapping without a real connection.
//
// A non-nil recvErr makes RecvMsg fail before decoding a message, for cases
// that must never reach Validate().
type fakeServerStream struct {
	ctx     context.Context
	recvErr error
}

func (m *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (m *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (m *fakeServerStream) SetTrailer(metadata.MD)       {}
func (m *fakeServerStream) Context() context.Context     { return m.ctx }
func (m *fakeServerStream) SendMsg(any) error            { return nil }
func (m *fakeServerStream) RecvMsg(any) error            { return m.recvErr }

// Test_ValidateUnaryInterceptor_Outcomes verifies that only a request whose
// validation fails is rejected, always with InvalidArgument.
func Test_ValidateUnaryInterceptor_Outcomes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		req            any
		wantHandlerRun bool
		wantMessage    string
	}{
		{
			name:           "invalid request rejected before handler",
			req:            &fakeValidatable{err: errors.New("bad request")},
			wantHandlerRun: false,
			wantMessage:    "bad request",
		},
		{
			name:           "status error from Validate becomes InvalidArgument",
			req:            &fakeValidatable{err: status.Error(codes.NotFound, "missing")},
			wantHandlerRun: false,
			wantMessage:    "rpc error: code = NotFound desc = missing",
		},
		{
			name:           "message without Validate passes through",
			req:            struct{}{},
			wantHandlerRun: true,
		},
		{
			name:           "valid request reaches handler",
			req:            &fakeValidatable{},
			wantHandlerRun: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := xgrpc.ValidateUnaryInterceptor()

			handlerCalled := false
			handler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "ok", nil
			}

			resp, err := interceptor(t.Context(), tc.req, &grpc.UnaryServerInfo{}, handler)

			require.Equal(t, tc.wantHandlerRun, handlerCalled)
			if tc.wantMessage == "" {
				require.NoError(t, err)
				require.Equal(t, "ok", resp)
				return
			}
			require.Nil(t, resp)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.EqualError(t, err, "rpc error: code = InvalidArgument desc = "+tc.wantMessage)
		})
	}
}

// Test_ValidateStreamInterceptor_Outcomes verifies that the wrapped receive
// rejects only a decoded message whose validation fails.
func Test_ValidateStreamInterceptor_Outcomes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		recvErr     error
		msg         any
		wantMessage string
	}{
		{
			name:        "invalid message rejected on RecvMsg",
			msg:         &fakeValidatable{err: errors.New("bad message")},
			wantMessage: "bad message",
		},
		{
			name: "message without Validate passes through",
			msg:  &struct{}{},
		},
		{
			name: "valid message passes through",
			msg:  &fakeValidatable{},
		},
		{
			name:    "RecvMsg failure passes through without validating",
			recvErr: io.EOF,
			// Would fail Validate() if the wrapper ran it despite the
			// RecvMsg error.
			msg: &fakeValidatable{err: errors.New("must not run")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := xgrpc.ValidateStreamInterceptor()

			var recvErr error
			handler := func(srv any, stream grpc.ServerStream) error {
				recvErr = stream.RecvMsg(tc.msg)
				return nil
			}

			err := interceptor(nil, &fakeServerStream{ctx: t.Context(), recvErr: tc.recvErr}, &grpc.StreamServerInfo{}, handler)
			require.NoError(t, err)

			if tc.recvErr != nil {
				require.ErrorIs(t, recvErr, tc.recvErr)
				return
			}
			if tc.wantMessage == "" {
				require.NoError(t, recvErr)
				return
			}
			require.Equal(t, codes.InvalidArgument, status.Code(recvErr))
			require.EqualError(t, recvErr, "rpc error: code = InvalidArgument desc = "+tc.wantMessage)
		})
	}
}

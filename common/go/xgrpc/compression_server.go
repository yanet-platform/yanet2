package xgrpc

import (
	"context"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/gzip"
)

// ResponseCompressionUnaryInterceptor compresses the response with the best
// content coding the client advertises: zstd, else gzip, else none.
//
// A client advertising nothing gets the default of the server, which answers
// as the request was sent.
func ResponseCompressionUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		selectResponseCompressor(ctx)
		return handler(ctx, req)
	}
}

// ResponseCompressionStreamInterceptor does for the responses of a stream what
// ResponseCompressionUnaryInterceptor does for a response.
func ResponseCompressionStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		selectResponseCompressor(stream.Context())
		return handler(srv, stream)
	}
}

// selectResponseCompressor sets the response coding before any header leaves;
// a failure to set it keeps the default of the server.
func selectResponseCompressor(ctx context.Context) {
	advertised, err := grpc.ClientSupportedCompressors(ctx)
	if err != nil {
		return
	}

	// An absent header comes back as a single empty name.
	advertised = slices.DeleteFunc(advertised, func(name string) bool { return name == "" })
	if len(advertised) == 0 {
		return
	}

	for _, name := range []string{ZstdName, gzip.Name} {
		if slices.Contains(advertised, name) && grpc.SetSendCompressor(ctx, name) == nil {
			return
		}
	}

	_ = grpc.SetSendCompressor(ctx, encoding.Identity)
}

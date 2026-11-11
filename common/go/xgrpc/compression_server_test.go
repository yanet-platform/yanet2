package xgrpc_test

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// checkCall makes one call of path over plain HTTP/2 with exactly the given
// coding headers, and returns the content coding the response names.
func checkCall(t *testing.T, address, path string, header http.Header) string {
	t.Helper()

	protocols := &http.Protocols{}
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}

	// An empty, uncompressed message.
	body := strings.NewReader("\x00\x00\x00\x00\x00")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+path, body)
	require.NoError(t, err)
	request.Header = header.Clone()
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("TE", "trailers")

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.Equal(t, "0", response.Trailer.Get("Grpc-Status"))

	return response.Header.Get("Grpc-Encoding")
}

// Test_ResponseCompression_FollowsAdvertisedCodings verifies that a server
// answers in zstd, else gzip, else uncompressed, according to what the client
// advertises, and as the request header names when it advertises nothing.
func Test_ResponseCompression_FollowsAdvertisedCodings(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// A stream handler that sends one message of two bytes and ends.
	serving := &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(xgrpc.ResponseCompressionUnaryInterceptor()),
		grpc.ChainStreamInterceptor(xgrpc.ResponseCompressionStreamInterceptor()),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error { return stream.SendMsg(serving) }),
	)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"zstd and gzip, spaced", http.Header{"Grpc-Accept-Encoding": {"zstd, gzip"}}, "zstd"},
		{"gzip before zstd", http.Header{"Grpc-Accept-Encoding": {"gzip,zstd"}}, "zstd"},
		{"gzip only", http.Header{"Grpc-Accept-Encoding": {"gzip"}}, "gzip"},
		{"identity only, gzip request", http.Header{"Grpc-Accept-Encoding": {"identity"}, "Grpc-Encoding": {"gzip"}}, "identity"},
		{"nothing, gzip request", http.Header{"Grpc-Encoding": {"gzip"}}, "gzip"},
	}

	// The unary path and the stream path each have an interceptor of their own.
	for _, path := range []string{"/grpc.health.v1.Health/Check", "/yanet.test/Stream"} {
		for _, tc := range cases {
			t.Run(tc.name+path, func(t *testing.T) {
				require.Equal(t, tc.want, checkCall(t, listener.Addr().String(), path, tc.header))
			})
		}
	}
}

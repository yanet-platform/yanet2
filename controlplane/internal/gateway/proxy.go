package gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/siderolabs/grpc-proxy/proxy"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

// TransparentWebGRPCProxy is an HTTP handler that translates HTTP requests
// with binary protobuf payloads to gRPC calls.
type TransparentWebGRPCProxy struct {
	registry *BackendRegistry
	log      *zap.SugaredLogger
}

// NewHTTPHandler creates a new HTTPHandler.
func NewHTTPHandler(registry *BackendRegistry, log *zap.SugaredLogger) *TransparentWebGRPCProxy {
	return &TransparentWebGRPCProxy{
		registry: registry,
		log:      log,
	}
}

// ServeHTTP handles HTTP requests.
func (m *TransparentWebGRPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only support POST method for actual gRPC calls.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed. Only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	// Convert URL path to gRPC fullMethodName format.
	fullMethodName := r.URL.Path
	fullMethodName = strings.TrimPrefix(fullMethodName, "/api")
	if !strings.HasPrefix(fullMethodName, "/") {
		fullMethodName = "/" + fullMethodName
	}

	service, method, err := xgrpc.ParseFullMethod(fullMethodName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid method name: %v", err), http.StatusBadRequest)
		return
	}

	backend, ok := m.registry.GetBackend(service)
	if !ok {
		http.Error(w, fmt.Sprintf("Service not found: %s", service), http.StatusNotFound)
		return
	}

	// Read request body (binary protobuf payload).
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	// Create context with metadata from HTTP headers.
	ctx := r.Context()
	md := metadata.New(nil)
	for k, v := range r.Header {
		k = strings.ToLower(k)
		if !strings.HasPrefix(k, "sec-") && k != "host" && k != "connection" {
			md.Set(k, v...)
		}
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	outCtx, conn, err := backend.GetConnection(ctx, fullMethodName)
	if err != nil {
		m.log.Errorw("failed to get connection to backend",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		http.Error(w, fmt.Sprintf("Failed to connect to backend: %v", err), http.StatusInternalServerError)
		return
	}

	// We use a custom codec to directly send/receive binary protobuf data.
	responseBuffer := proxy.NewFrame(nil)

	// Invoke the gRPC method with the binary protobuf payload.
	err = conn.Invoke(
		outCtx,
		fullMethodName,
		proxy.NewFrame(body),
		responseBuffer,
		grpc.ForceCodecV2(proxy.Codec()),
	)
	if err != nil {
		m.log.Errorw("failed to proxy gRPC",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)

		// Convert gRPC error to HTTP error
		statusErr, ok := status.FromError(err)
		if ok {
			code := statusErr.Code()
			http.Error(w, statusErr.Message(), grpcCodeToHTTPStatus(code))
		} else {
			http.Error(w, fmt.Sprintf("failed to proxy gRPC: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// We need to re-marshal the response to get the binary data.
	codec := proxy.Codec()
	bufSlice, err := codec.Marshal(responseBuffer)
	if err != nil {
		m.log.Errorw("failed to marshal response",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		http.Error(w, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	// Materialize the buffer slice.
	respData := bufSlice.Materialize()

	// Set our special content type header.
	w.Header().Set("Content-Type", "application/x-protobuf")

	// Write the binary payload to the response.
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respData); err != nil {
		m.log.Errorw("failed to write HTTP response",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
	}
}

// grpcCodeToHTTPStatus maps gRPC status codes to HTTP status codes.
func grpcCodeToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.Canceled:
		return http.StatusRequestTimeout
	case codes.Unknown:
		return http.StatusInternalServerError
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return http.StatusBadRequest
	case codes.Aborted:
		return http.StatusConflict
	case codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Internal:
		return http.StatusInternalServerError
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DataLoss:
		return http.StatusInternalServerError
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

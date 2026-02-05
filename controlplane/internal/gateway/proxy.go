package gateway

import (
	"compress/gzip"
	"encoding/json"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

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

	// Check if this is a server streaming method and handle accordingly.
	if m.isServerStreaming(fullMethodName) {
		m.handleServerStreaming(w, r, fullMethodName)
		return
	}

	// Handle unary gRPC calls.
	m.handleUnary(w, r, fullMethodName)
}

// handleUnary handles unary gRPC calls.
func (m *TransparentWebGRPCProxy) handleUnary(w http.ResponseWriter, r *http.Request, fullMethodName string) {
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	requestMsg, err := m.jsonToProtobuf(fullMethodName, body, true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse JSON request: %v", err), http.StatusBadRequest)
		return
	}
	protobufBody, err := proto.Marshal(requestMsg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal protobuf: %v", err), http.StatusInternalServerError)
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
		proxy.NewFrame(protobufBody),
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
	responseMessage, err := m.protobufToMessage(fullMethodName, respData, false)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to unmarshal protobuf response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(responseMessage); err != nil {
		m.log.Errorw("failed to encode JSON response",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
	}
}

// jsonToProtobuf converts JSON to protobuf message for the given method.
func (m *TransparentWebGRPCProxy) jsonToProtobuf(fullMethodName string, jsonData []byte, isRequest bool) (proto.Message, error) {
	ty, err := m.getMessageType(fullMethodName, isRequest)
	if err != nil {
		return nil, err
	}

	msg := ty.New().Interface()
	if err := json.Unmarshal(jsonData, msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return msg, nil
}

// protobufToMessage converts protobuf bytes to message.
func (m *TransparentWebGRPCProxy) protobufToMessage(fullMethodName string, protobufData []byte, isRequest bool) (proto.Message, error) {
	ty, err := m.getMessageType(fullMethodName, isRequest)
	if err != nil {
		return nil, err
	}

	msg := ty.New().Interface()
	if err := proto.Unmarshal(protobufData, msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}

	return msg, nil
}

// getMethodDescriptor returns the protobuf method descriptor for the given method.
func (m *TransparentWebGRPCProxy) getMethodDescriptor(fullMethodName string) (protoreflect.MethodDescriptor, error) {
	service, method, err := xgrpc.ParseFullMethod(fullMethodName)
	if err != nil {
		return nil, err
	}

	serviceName := protoreflect.FullName(service)
	serviceDesc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		return nil, fmt.Errorf("service not found: %s", service)
	}

	svcDesc, ok := serviceDesc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("descriptor is not a service: %s", service)
	}

	methodDesc := svcDesc.Methods().ByName(protoreflect.Name(method))
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found: %s.%s", service, method)
	}

	return methodDesc, nil
}

// isServerStreaming checks if the given method is a server streaming method.
func (m *TransparentWebGRPCProxy) isServerStreaming(fullMethodName string) bool {
	methodDesc, err := m.getMethodDescriptor(fullMethodName)
	if err != nil {
		return false
	}
	return methodDesc.IsStreamingServer()
}

// getMessageType returns the protobuf message type for the given method.
func (m *TransparentWebGRPCProxy) getMessageType(fullMethodName string, isRequest bool) (protoreflect.MessageType, error) {
	methodDesc, err := m.getMethodDescriptor(fullMethodName)
	if err != nil {
		return nil, err
	}

	var msgDesc protoreflect.MessageDescriptor
	if isRequest {
		msgDesc = methodDesc.Input()
	} else {
		msgDesc = methodDesc.Output()
	}

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(msgDesc.FullName())
	if err != nil {
		return nil, fmt.Errorf("message type not found: %s", msgDesc.FullName())
	}

	return msgType, nil
}

// handleServerStreaming handles server streaming gRPC calls via SSE.
func (m *TransparentWebGRPCProxy) handleServerStreaming(w http.ResponseWriter, r *http.Request, fullMethodName string) {
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	requestMsg, err := m.jsonToProtobuf(fullMethodName, body, true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse JSON request: %v", err), http.StatusBadRequest)
		return
	}
	protobufBody, err := proto.Marshal(requestMsg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal protobuf: %v", err), http.StatusInternalServerError)
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

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a client stream.
	streamDesc := &grpc.StreamDesc{
		StreamName:    method,
		ServerStreams: true,
	}

	clientStream, err := conn.NewStream(outCtx, streamDesc, fullMethodName, grpc.ForceCodecV2(proxy.Codec()))
	if err != nil {
		m.log.Errorw("failed to create stream",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		m.writeSSEError(w, err)
		return
	}

	// Send the request message.
	if err := clientStream.SendMsg(proxy.NewFrame(protobufBody)); err != nil {
		m.log.Errorw("failed to send request",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		m.writeSSEError(w, err)
		return
	}

	// Close the send side of the stream.
	if err := clientStream.CloseSend(); err != nil {
		m.log.Errorw("failed to close send",
			zap.String("service", service),
			zap.String("method", method),
			zap.Error(err),
		)
		m.writeSSEError(w, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	codec := proxy.Codec()

	// Read streaming responses.
	for {
		responseBuffer := proxy.NewFrame(nil)
		err := clientStream.RecvMsg(responseBuffer)
		if err == io.EOF {
			// Stream ended normally.
			m.writeSSEEvent(w, "end", "{}")
			flusher.Flush()
			return
		}
		if err != nil {
			m.log.Errorw("failed to receive stream message",
				zap.String("service", service),
				zap.String("method", method),
				zap.Error(err),
			)
			m.writeSSEError(w, err)
			flusher.Flush()
			return
		}

		// Marshal the response to get binary data.
		bufSlice, err := codec.Marshal(responseBuffer)
		if err != nil {
			m.log.Errorw("failed to marshal response",
				zap.String("service", service),
				zap.String("method", method),
				zap.Error(err),
			)
			continue
		}

		respData := bufSlice.Materialize()
		responseMessage, err := m.protobufToMessage(fullMethodName, respData, false)
		if err != nil {
			m.log.Errorw("failed to unmarshal protobuf response",
				zap.String("service", service),
				zap.String("method", method),
				zap.Error(err),
			)
			continue
		}

		jsonData, err := json.Marshal(responseMessage)
		if err != nil {
			m.log.Errorw("failed to marshal JSON response",
				zap.String("service", service),
				zap.String("method", method),
				zap.Error(err),
			)
			continue
		}

		m.writeSSEEvent(w, "message", string(jsonData))
		flusher.Flush()
	}
}

// writeSSEEvent writes an SSE event to the response writer.
func (m *TransparentWebGRPCProxy) writeSSEEvent(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// writeSSEError writes an error as an SSE event.
func (m *TransparentWebGRPCProxy) writeSSEError(w http.ResponseWriter, err error) {
	statusErr, ok := status.FromError(err)
	errorData := struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{
		Code:    int(codes.Unknown),
		Message: err.Error(),
	}
	if ok {
		errorData.Code = int(statusErr.Code())
		errorData.Message = statusErr.Message()
	}

	jsonData, _ := json.Marshal(errorData)
	m.writeSSEEvent(w, "error", string(jsonData))
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

// gzipResponseWriter wraps http.ResponseWriter to provide gzip compression.
type gzipResponseWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (m *gzipResponseWriter) Write(b []byte) (int, error) {
	return m.Writer.Write(b)
}

// Flush flushes buffered data to the client for SSE streaming support.
func (m *gzipResponseWriter) Flush() {
	// Flush gzip writer first to ensure compressed data is written.
	if gz, ok := m.Writer.(*gzip.Writer); ok {
		gz.Flush()
	}
	// Then flush the underlying ResponseWriter.
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// GzipMiddleware compresses HTTP responses and decompresses requests.
// It handles both request decompression (Content-Encoding: gzip) and
// response compression (Accept-Encoding: gzip).
// Requests without compression are handled normally for backward compatibility.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decompress request body if gzip encoded.
		if r.Header.Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "failed to decompress request", http.StatusBadRequest)
				return
			}
			defer gr.Close()
			r.Body = io.NopCloser(gr)
			r.Header.Del("Content-Encoding")
		}

		// Compress response if client accepts gzip.
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, Writer: gz}, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

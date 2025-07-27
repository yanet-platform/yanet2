package framework

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
)

// SocketClient handles communication with QEMU socket networking
type SocketClient struct {
	conn       net.Conn
	port       int
	socketPath string
	timeout    time.Duration
	log        *zap.SugaredLogger
}

// SocketClientOption defines functional options for SocketClient
type SocketClientOption func(*SocketClient) error

// WithTimeout sets the timeout for read/write operations
func WithTimeout(timeout time.Duration) SocketClientOption {
	return func(sc *SocketClient) error {
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive, got: %v", timeout)
		}
		sc.timeout = timeout
		return nil
	}
}

// SocketClientWithLog sets the logger for the SocketClient
func SocketClientWithLog(log *zap.SugaredLogger) SocketClientOption {
	return func(sc *SocketClient) error {
		sc.log = log
		return nil
	}
}

// NewSocketClientTCP creates a new socket client for connecting to QEMU TCP socket networking
func NewSocketClientTCP(port int, opts ...SocketClientOption) (*SocketClient, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port number: %d", port)
	}

	sc := &SocketClient{
		port:    port,
		timeout: 1 * time.Second,      // default 1s timeout
		log:     zap.NewNop().Sugar(), // default noop logger
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(sc); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return sc, nil
}

// NewSocketClient creates a new socket client for connecting to Unix socket (legacy)
func NewSocketClient(socketPath string, opts ...SocketClientOption) (*SocketClient, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("socket path cannot be empty")
	}

	sc := &SocketClient{
		socketPath: socketPath,
		timeout:    5 * time.Second,      // default 5s timeout for Unix sockets
		log:        zap.NewNop().Sugar(), // default noop logger
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(sc); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return sc, nil
}

// Connect establishes connection to QEMU socket (TCP or Unix)
func (sc *SocketClient) Connect() error {
	var err error

	if sc.conn != nil {
		return nil
	}

	// Try to connect with retries
	for i := range 10 {
		if sc.socketPath != "" {
			// Unix socket connection (legacy)
			sc.conn, err = net.Dial("unix", sc.socketPath)
			if err == nil {
				sc.log.Infof("Connected to Unix socket at %s", sc.socketPath)
				return nil
			}
			sc.log.Warnf("Unix socket connection attempt %d failed: %v, retrying...", i+1, err)
		} else {
			// TCP socket connection to QEMU
			sc.conn, err = net.Dial("tcp", fmt.Sprintf("localhost:%d", sc.port))
			if err == nil {
				sc.log.Infof("Connected to TCP socket on port %d", sc.port)
				return nil
			}
			sc.log.Warnf("TCP connection attempt %d failed: %v, retrying...", i+1, err)
		}

		time.Sleep(2 * time.Second)
	}

	if sc.socketPath != "" {
		return fmt.Errorf("failed to connect to Unix socket %s after 10 attempts: %w", sc.socketPath, err)
	} else {
		return fmt.Errorf("failed to connect to TCP socket on port %d after 10 attempts: %w", sc.port, err)
	}
}

// SendPacket sends a raw packet through the QEMU socket connection
// The packet is prefixed with its length in network byte order
func (sc *SocketClient) SendPacket(packet []byte) error {
	if sc.conn == nil {
		return fmt.Errorf("not connected to socket")
	}

	err := sc.conn.SetWriteDeadline(time.Now().Add(sc.timeout))
	if err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Create a buffer with the packet length in network byte order followed by the packet data
	packetWithLength := make([]byte, 4+len(packet))
	binary.BigEndian.PutUint32(packetWithLength, uint32(len(packet)))
	copy(packetWithLength[4:], packet)

	sc.log.Infof("Sending packet with length prefix: % x", packetWithLength)

	_, err = sc.conn.Write(packetWithLength)
	if err != nil {
		return fmt.Errorf("failed to send packet: %w", err)
	}

	return nil
}

// ReceivePacket receives a packet from the QEMU socket connection
// The packet is expected to be prefixed with its length in network byte order
func (sc *SocketClient) ReceivePacket() ([]byte, error) {
	if sc.conn == nil {
		return nil, fmt.Errorf("not connected to socket")
	}

	// Create packet parser for filtering
	parser := NewPacketParser()
	ourMAC := MustParseMAC(SrcMAC)

	// Keep reading packets until we find one with the correct SrcMAC
	for {
		err := sc.conn.SetReadDeadline(time.Now().Add(sc.timeout))
		if err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		// Read the packet length prefix (4 bytes)
		lengthPrefix := make([]byte, 4)
		_, err = sc.conn.Read(lengthPrefix)
		if err != nil {
			return nil, fmt.Errorf("failed to read packet length prefix: %w", err)
		}
		sc.log.Infof("Received packet length prefix: % x", lengthPrefix)

		packetLength := binary.BigEndian.Uint32(lengthPrefix)
		if packetLength > 9000 {
			return nil, fmt.Errorf("packet length %d exceeds maximum buffer size", packetLength)
		}

		// Read the packet data
		packetData := make([]byte, packetLength)
		_, err = sc.conn.Read(packetData)
		if err != nil {
			return nil, fmt.Errorf("failed to read packet data: %w", err)
		}
		sc.log.Infof("Received packet data: % x", packetData)

		// Parse the packet to check SrcMAC
		packetInfo, err := parser.ParsePacket(packetData)
		if err != nil {
			sc.log.Warnf("Failed to parse packet: %v", err)
			// Continue reading packets
			continue
		}

		// Check if the packet has the correct SrcMAC
		if packetInfo.DstMAC.String() == ourMAC.String() {
			return packetData, nil
		}

		// Skip packets with incorrect SrcMAC
		sc.log.Infof("Skipping packet with incorrect DstMAC: %s (expected: %s)", packetInfo.DstMAC, ourMAC)
	}
}

// Close closes the socket connection
func (sc *SocketClient) Close() error {
	if sc.conn != nil {
		return sc.conn.Close()
	}
	return nil
}

// GetSocketPort returns the socket port for this client
func (sc *SocketClient) GetSocketPort() int {
	return sc.port
}

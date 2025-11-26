package lib

import "time"

// Constants for converter configuration and limits
const (
	// ASTParserTimeout is the maximum time to wait for Python AST parser execution
	ASTParserTimeout = 30 * time.Second

	// HexDumpContextBytes is the number of bytes to show around differences in hex dumps
	HexDumpContextBytes = 16

	// MinIPv4HeaderSize is the minimum size of an IPv4 header in bytes
	MinIPv4HeaderSize = 20

	// MaxSkiplistFileSize is the maximum allowed size for skiplist.yaml file (10 MB)
	MaxSkiplistFileSize = 10 * 1024 * 1024

	// MaxPacketsPerFile is the maximum number of packets to read from a single PCAP file
	MaxPacketsPerFile = 10000

	// DefaultPacketTimeout is the default timeout for packet send/receive operations
	DefaultPacketTimeout = 100 // milliseconds

	// ConfigSetupDelay is the delay after configuration before sending first packet
	ConfigSetupDelay = 3 * time.Second
)

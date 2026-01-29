package bird

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

// TestParserNext tests the Parser.Next() method which reads chunk size and data.
// This test reproduces the bug where BIRD writes chunk size EXCLUDING the 4-byte size field,
// but the old parser code was incorrectly subtracting 4 from it again.
func TestParserNext(t *testing.T) {
	// Use existing test data from update_test.go
	// This data is 64 bytes (update struct without the chunk size prefix)
	updateData := []byte{
		// NetAddrUnion 40 bytes
		0: 0x2,     // NetAddr type NetIP6
		1: 0x30,    // prefix len == 48
		2: 0x14, 0, // NetAddr struct size
		4: 0xb8, 0xd, 0x7, 0x23, 0, 0, 0x4, 0, 0, 0, 0, 0, 0, 0, 0, 0, // prefix
		// garbage
		20: 0, 0, 0, 0, 0x98, 0xd2, 0xa3, 0x35, 0x47, 0x59,
		30: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		// update type LE u32
		40: 0x1, 0, 0, 0,
		// peer addr
		44: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		// attrsAreaSize EXCLUDING sizeof(attrsAreaSize) - 0x0 => no attributes
		60: 0x0, 0, 0, 0x0,
	}

	// BIRD protocol: chunk size EXCLUDES the 4-byte size field itself
	// So for 64 bytes of data, BIRD writes chunk_size=64
	chunkSize := uint32(len(updateData))
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, chunkSize)
	buf.Write(updateData)

	reader := bytes.NewReader(buf.Bytes())
	parser := NewParser(reader, 4096, zaptest.NewLogger(t).Sugar())

	// With the fix, this should work correctly
	update, err := parser.Next()
	require.NoError(t, err, "Parser should correctly handle BIRD's chunk size format")
	require.NotNil(t, update)

	// The update should be parseable
	route := &rib.Route{}
	err = update.Decode(route)
	require.NoError(t, err, "Update should decode successfully")
	require.Equal(t, "2307:db8:4::/48", route.Prefix.String())
}

package xgrpc_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/encoding"
)

// lines returns the text of the libzstd fixture, cut to count lines.
func lines(count int) []byte {
	var text bytes.Buffer
	for idx := range count {
		fmt.Fprintf(&text, "line %06d: yanet gateway config\n", idx)
	}

	return text.Bytes()
}

// unpack decompresses data with the registered zstd compressor.
func unpack(t *testing.T, data []byte) []byte {
	t.Helper()

	reader, err := encoding.GetCompressor("zstd").Decompress(bytes.NewReader(data))
	require.NoError(t, err)
	unpacked, err := io.ReadAll(reader)
	require.NoError(t, err)

	return unpacked
}

// Test_ZstdCompressor_RoundTripAtLevelThree verifies that messages of any size
// come back unchanged when callers share the pooled coders, and that a message
// of one block is written as the reference level 3 encoder writes it.
func Test_ZstdCompressor_RoundTripAtLevelThree(t *testing.T) {
	for _, count := range []int{1, 3000, 100000} {
		t.Run(fmt.Sprintf("%d lines", count), func(t *testing.T) {
			t.Parallel()

			var packed bytes.Buffer
			writer, err := encoding.GetCompressor("zstd").Compress(&packed)
			require.NoError(t, err)
			data := lines(count)
			_, err = writer.Write(data)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			require.True(t, bytes.Equal(data, unpack(t, packed.Bytes())))

			if count == 3000 {
				reference, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)))
				require.NoError(t, err)
				require.Equal(t, reference.EncodeAll(data, nil), packed.Bytes())
			}
		})
	}
}

// Test_ZstdCompressor_DecompressesLibzstdFrame verifies that a frame written
// by libzstd without stating its size decompresses to the message.
//
// The fixture comes from zstd 1.5.7 reading a pipe: seq 0 19999 |
// awk '{printf "line %06d: yanet gateway config\n", $1}' | zstd -3 -c -q
func Test_ZstdCompressor_DecompressesLibzstdFrame(t *testing.T) {
	frame, err := os.ReadFile("testdata/libzstd_no_content_size.zst")
	require.NoError(t, err)

	require.True(t, bytes.Equal(lines(20000), unpack(t, frame)))
}

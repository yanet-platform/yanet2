package xgrpc_test

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/encoding"
)

// lines returns count lines of compressible text.
func lines(count int) []byte {
	var text bytes.Buffer
	for idx := range count {
		fmt.Fprintf(&text, "line %06d: yanet gateway config\n", idx)
	}

	return text.Bytes()
}

// pack compresses data with the registered zstd compressor.
func pack(t *testing.T, data []byte) []byte {
	t.Helper()

	var packed bytes.Buffer
	writer, err := encoding.GetCompressor("zstd").Compress(&packed)
	require.NoError(t, err)
	_, err = writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return packed.Bytes()
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

			data := lines(count)
			packed := pack(t, data)
			require.True(t, bytes.Equal(data, unpack(t, packed)))

			if count == 3000 {
				reference, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)))
				require.NoError(t, err)
				require.Equal(t, reference.EncodeAll(data, nil), packed)
			}
		})
	}
}

// allocatedDuring returns the bytes call allocated, counting every allocation
// whether or not it was freed again.
func allocatedDuring(call func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	call()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

// Test_ZstdCompressor_DecompressRefusesWindowBeyondCap verifies that a frame
// declaring a history beyond the cap is refused without reserving it, whether
// the history is the content of a single segment or a window.
func Test_ZstdCompressor_DecompressRefusesWindowBeyondCap(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
	}{
		{"single segment of 200 MiB", []byte{0xa0, 0x00, 0x00, 0x80, 0x0c}},
		{"window of 128 MiB", []byte{0x00, 0x88}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One run-length block follows the header, which starts the decoder.
			frame := append([]byte{0x28, 0xb5, 0x2f, 0xfd}, tc.header...)
			frame = append(frame, 0x03, 0x00, 0x10, 0x00)

			var err error
			allocated := allocatedDuring(func() {
				var reader io.Reader
				if reader, err = encoding.GetCompressor("zstd").Decompress(bytes.NewReader(frame)); err == nil {
					_, err = io.ReadAll(reader)
				}
			})

			require.Error(t, err)
			require.Less(t, allocated, uint64(1<<20))
		})
	}
}

// Test_ZstdCompressor_DecompressReleasesAbandonedReader verifies that a
// decoder is pooled again when its reader is dropped half read, as grpc drops
// it at a receive limit.
//
// A pooled decoder makes the next read cost a fraction of a new one, which the
// test waits for across collections, as a pool may also drop what it is given.
func Test_ZstdCompressor_DecompressReleasesAbandonedReader(t *testing.T) {
	frame := pack(t, lines(20000))

	halfRead := func() (reader io.Reader, allocated uint64) {
		allocated = allocatedDuring(func() {
			var err error
			reader, err = encoding.GetCompressor("zstd").Decompress(bytes.NewReader(frame))
			require.NoError(t, err)
			_, err = reader.Read(make([]byte, 1))
			require.NoError(t, err)
		})

		return reader, allocated
	}

	// Drain the pool, keeping the readers so that none releases its decoder,
	// until a read has to build one: a new decoder costs megabytes.
	var held []io.Reader
	for allocated := uint64(0); allocated < 1<<20; {
		require.Less(t, len(held), 1000, "no read has built a decoder")

		var reader io.Reader
		reader, allocated = halfRead()
		held = append(held, reader)
	}
	held = nil

	for range 100 {
		runtime.GC()
		time.Sleep(time.Millisecond)
		if _, allocated := halfRead(); allocated < 64<<10 {
			return
		}
	}

	t.Fatal("the abandoned decoder was never pooled again")
}

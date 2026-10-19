package xgrpc

import (
	"io"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/grpc/encoding"
)

// ZstdName is the content coding of the zstd compressor registered here.
const ZstdName = "zstd"

// zstdMaxWindowSize bounds the history a frame may ask the decoder to keep.
//
// A decoder reserves the window when it starts on the first block, and
// decompression runs before authentication, so the default of 512 MiB would
// let a tiny frame claim that much. Level 3 frames use a window of at most
// 8 MiB from klauspost and of 2 MiB from libzstd.
const zstdMaxWindowSize = 8 << 20

func init() {
	encoding.RegisterCompressor(&zstdCompressor{})
}

// zstdCompressor is the gRPC compressor for the zstd content coding.
//
// Messages are coded as streams, so a receiver reads no more of a message
// than its own limit allows.
type zstdCompressor struct {
	encoders sync.Pool
	decoders sync.Pool
}

func (m *zstdCompressor) Name() string {
	return ZstdName
}

// Compress returns a writer that compresses into w at zstd level 3.
func (m *zstdCompressor) Compress(w io.Writer) (io.WriteCloser, error) {
	encoder, ok := m.encoders.Get().(*zstd.Encoder)
	if !ok {
		var err error
		encoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)), zstd.WithEncoderConcurrency(1))
		if err != nil {
			return nil, err
		}
	}

	encoder.Reset(w)

	return &pooledEncoder{Encoder: encoder, pool: &m.encoders}, nil
}

// Decompress returns a reader of the data compressed in r.
func (m *zstdCompressor) Decompress(r io.Reader) (io.Reader, error) {
	decoder, ok := m.decoders.Get().(*zstd.Decoder)
	if !ok {
		var err error
		decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(zstdMaxWindowSize))
		if err != nil {
			return nil, err
		}
	}

	slot := &decoderSlot{decoder: decoder, pool: &m.decoders}
	if err := decoder.Reset(r); err != nil {
		slot.Release()
		return nil, err
	}

	reader := &pooledDecoder{decoderSlot: slot}
	runtime.AddCleanup(reader, (*decoderSlot).Release, slot)

	return reader, nil
}

// pooledEncoder returns its encoder to the pool once the frame is closed.
//
// A writer that fails is dropped without being closed. The encoder has one
// slot and so runs no goroutine, and the garbage collector takes it whole.
type pooledEncoder struct {
	*zstd.Encoder
	pool   *sync.Pool
	closed bool
	err    error
}

func (m *pooledEncoder) Close() error {
	if m.closed {
		return m.err
	}

	m.closed = true
	m.err = m.Encoder.Close()
	m.Encoder.Reset(nil)
	m.pool.Put(m.Encoder)

	return m.err
}

// decoderSlot owns a pooled decoder until it is released.
//
// The release happens once, either by the reader that finishes with the
// decoder or by the cleanup of a reader dropped half read, as grpc drops it
// when a message passes its receive limit.
type decoderSlot struct {
	decoder  *zstd.Decoder
	pool     *sync.Pool
	released atomic.Bool
}

func (m *decoderSlot) Release() {
	if m.released.CompareAndSwap(false, true) {
		_ = m.decoder.Reset(nil)
		m.pool.Put(m.decoder)
	}
}

// pooledDecoder releases its decoder once the stream ends or fails.
type pooledDecoder struct {
	*decoderSlot
	err error
}

func (m *pooledDecoder) Read(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}

	n, err := m.decoder.Read(p)
	if err != nil {
		m.err = err
		m.Release()
	}

	return n, err
}

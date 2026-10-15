package xgrpc

import (
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/grpc/encoding"
)

// ZstdName is the content coding of the zstd compressor registered here.
const ZstdName = "zstd"

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
		decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, err
		}
	}

	if err := decoder.Reset(r); err != nil {
		return nil, err
	}

	return &pooledDecoder{decoder: decoder, pool: &m.decoders}, nil
}

// pooledEncoder returns its encoder to the pool once the frame is closed.
type pooledEncoder struct {
	*zstd.Encoder
	pool *sync.Pool
}

func (m *pooledEncoder) Close() error {
	err := m.Encoder.Close()

	m.Encoder.Reset(nil)
	m.pool.Put(m.Encoder)

	return err
}

// pooledDecoder returns its decoder to the pool once the stream ends or fails.
type pooledDecoder struct {
	decoder *zstd.Decoder
	pool    *sync.Pool
	err     error
}

func (m *pooledDecoder) Read(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}

	n, err := m.decoder.Read(p)
	if err != nil {
		m.err = err
		_ = m.decoder.Reset(nil)
		m.pool.Put(m.decoder)
	}

	return n, err
}

package bird

import (
	"encoding/binary"
	"fmt"
	"io"
	"unsafe"

	"go.uber.org/zap"
)

const (
	sizeOfUint32    = unsafe.Sizeof(uint32(0))
	sizeOfChunkSize = sizeOfUint32
)

type Parser struct {
	reader io.Reader
	buf    []byte
	log    *zap.SugaredLogger
}

func NewParser(r io.Reader, bufSize int, log *zap.SugaredLogger) *Parser {
	return &Parser{
		reader: r,
		buf:    make([]byte, bufSize),
		log:    log,
	}
}

func (m *Parser) readChunk(size int) error {
	if size > len(m.buf) {
		return fmt.Errorf("buffer too small want %d > bufsize %d", size, len(m.buf))
	}
	_, err := io.ReadFull(m.reader, m.buf[:size])

	return err
}

func (m *Parser) readChunkSize() (uint32, error) {
	if err := m.readChunk(int(sizeOfChunkSize)); err != nil {
		return 0, err
	}
	chunkSize := binary.LittleEndian.Uint32(m.buf[:sizeOfChunkSize])

	return chunkSize, nil
}

func (m *Parser) Next() (*update, error) {
	chunkSize, err := m.readChunkSize()
	if err != nil {
		return nil, fmt.Errorf("parser.readChunkSize: %w", err)
	}
	if chunkSize <= uint32(sizeOfChunkSize) {
		return nil, fmt.Errorf("too small chunk: %d", chunkSize)
	}
	readSize := chunkSize - uint32(sizeOfChunkSize)

	if err = m.readChunk(int(readSize)); err != nil {
		return nil, fmt.Errorf("m.readChunk(%d): %w", readSize, err)
	}

	return newUpdate(m.buf[:int(readSize)])
}

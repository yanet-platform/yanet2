package adapterpb

import (
	"errors"
	"fmt"
	"math"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxParserBufSize is the largest useful parser buffer: the chunk size field
// of the BIRD export stream is 32 bits wide.
const maxParserBufSize uint64 = math.MaxUint32

// Validate checks that the import configuration and both MPLS source addresses
// are present.
//
// The name must also be a valid module name and the configuration valid.
func (m *SetupConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if m.GetConfig() == nil {
		return errors.New("config is required")
	}
	if err := m.GetConfig().Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if m.GetSourceV4() == nil {
		return errors.New("source_v4 is required")
	}
	if m.GetSourceV6() == nil {
		return errors.New("source_v6 is required")
	}

	return nil
}

// Validate checks that at least one socket is given and that the dump and
// buffer values are in range, where zero selects the default.
func (m *ImportConfig) Validate() error {
	if len(m.GetSockets()) == 0 {
		return errors.New("sockets must contain at least one socket")
	}
	if m.GetDumpThreshold() < 0 {
		return fmt.Errorf("dump_threshold %d must not be negative", m.GetDumpThreshold())
	}
	if m.GetDumpTimeout() < 0 {
		return fmt.Errorf("dump_timeout %d must not be negative", m.GetDumpTimeout())
	}
	if m.GetParserBufSize() > maxParserBufSize {
		return fmt.Errorf("parser_buf_size %d must be in range 0..%d", m.GetParserBufSize(), maxParserBufSize)
	}

	return nil
}

package adapterpb

import (
	"slices"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/bird"
)

func (m *ImportConfig) ToConfig(cfg *bird.Config) {
	cfg.Sockets = slices.Clone(m.Sockets)
	if m.ParserBufSize != 0 {
		cfg.ParserBufSize = datasize.ByteSize(m.ParserBufSize)
	}
	if m.DumpThreshold != 0 {
		cfg.DumpThreshold = int(m.DumpThreshold)
	}
	if m.DumpTimeout != 0 {
		cfg.DumpTimeout = time.Duration(m.DumpTimeout)
	}
}

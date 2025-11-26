package adapterpb

import (
	"slices"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/bird"
)

func (m *ImportConfig) ToConfig(cfg *bird.Config) {
	cfg.Sockets = slices.Clone(m.Sockets)
	cfg.ParserBufSize = datasize.ByteSize(m.ParserBufSize)
	cfg.DumpThreshold = int(m.DumpThreshold)
	cfg.DumpTimeout = time.Duration(m.DumpTimeout)
}

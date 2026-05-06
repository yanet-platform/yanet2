package prometheus

import (
	"io"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type Undefine struct{}

func (*Undefine) IsValue() {}

func (*Undefine) Write(w io.Writer, n string, l []metric.Label) {
}

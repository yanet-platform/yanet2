package route

import (
	"fmt"

	"go.uber.org/zap"

	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

type ribHolder struct {
	rib *rib.RIB
}

func newRIBHolder(log *zap.SugaredLogger) *ribHolder {
	return &ribHolder{
		rib: rib.NewRIB(log),
	}
}

func (m *ribHolder) updateRIB(update *routepb.Update) error {
	route, err := routepb.ToRIBRoute(update.GetRoute(), update.GetIsDelete())
	if err != nil {
		return fmt.Errorf("failed to convert proto route to RIB route: %w", err)
	}
	m.rib.Update(*route)
	return nil
}

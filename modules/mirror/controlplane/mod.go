package mirror

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

const (
	moduleType = "mirror"
	agentName  = moduleType
)

// Option configures the MirrorModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the mirror module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// MirrorModule is a control-plane component of a module that is responsible for
// mirroring traffic between devices.
type MirrorModule struct {
	*modulebase.Base
}

func NewMirrorModule(cfg *Config, options ...Option) (*MirrorModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	mirrorService := NewMirrorService(NewBackend(base.Agent()))
	base.AddService(mirrorpb.MirrorService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		mirrorpb.RegisterMirrorServiceServer(server, mirrorService)
	})

	return &MirrorModule{Base: base}, nil
}

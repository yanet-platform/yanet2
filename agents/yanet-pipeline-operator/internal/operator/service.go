package operator

import (
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
)

// Service implements the PipelineOperatorService gRPC API.
type Service struct {
	operatorpb.UnimplementedPipelineOperatorServiceServer

	log *zap.Logger
}

func NewService(options ...ServiceOption) *Service {
	opts := newServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &Service{
		log: opts.Log,
	}
}

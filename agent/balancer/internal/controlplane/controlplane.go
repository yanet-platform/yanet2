package controlplane

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type Client struct {
	config Config

	client balancerpb.BalancerServiceClient
}

func New(config Config) (*Client, error) {
	grpcClient, err := grpc.NewClient(
		config.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize gRPC client: %w", err)
	}
	return &Client{
		config: config,
		client: balancerpb.NewBalancerServiceClient(grpcClient),
	}, nil
}

// AddService a new virtual service to the configuration.
func (c *Client) AddService(
	ctx context.Context,
	in *balancerpb.AddServiceRequest,
	opts ...grpc.CallOption,
) error {
	for instance := range c.config.InstanceCount {
		in.Target = &commonpb.TargetModule{
			ConfigName:        c.config.ModuleName,
			DataplaneInstance: instance,
		}
		_, err := c.client.AddService(ctx, in, opts...)
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoveService removes a virtual service from the configuration.
func (c *Client) RemoveService(
	ctx context.Context,
	in *balancerpb.RemoveServiceRequest,
	opts ...grpc.CallOption,
) error {
	for instance := range c.config.InstanceCount {
		in.Target = &commonpb.TargetModule{
			ConfigName:        c.config.ModuleName,
			DataplaneInstance: instance,
		}
		_, err := c.client.RemoveService(ctx, in, opts...)
		if err != nil {
			return err
		}
	}
	return nil
}

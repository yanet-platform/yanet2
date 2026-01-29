package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/proxy/controlplane/proxypb"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ProxyService struct {
	proxypb.UnimplementedProxyServiceServer

	mu      sync.Mutex
	log     *zap.SugaredLogger
	agent   *ffi.Agent
	configs map[string]*ProxyConfig
}

func NewProxyService(agent *ffi.Agent, log *zap.SugaredLogger) *ProxyService {
	return &ProxyService{
		log:     log,
		agent:   agent,
		configs: make(map[string]*ProxyConfig),
	}
}

func (s *ProxyService) ListConfigs(
	ctx context.Context, request *proxypb.ListConfigsRequest,
) (*proxypb.ListConfigsResponse, error) {
	response := &proxypb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	// Lock instances store and module updates
	s.mu.Lock()
	defer s.mu.Unlock()

	for name := range s.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (s *ProxyService) ShowConfig(ctx context.Context, req *proxypb.ShowConfigRequest) (*proxypb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// key := instanceKey{name: name, dataplaneInstance: inst}
	response := &proxypb.ShowConfigResponse{}

	s.mu.Lock()
	defer s.mu.Unlock()

	config := s.configs[name]
	if config != nil {
		response.Config = &proxypb.Config{
			Addr: config.Addr,
		}
	}

	return response, nil
}

func (s *ProxyService) DeleteConfig(ctx context.Context, req *proxypb.DeleteConfigRequest) (*proxypb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	// Remove module configuration from the control plane.
	delete(s.configs, name)

	deleted := DeleteConfig(s, name)

	response := &proxypb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}

func (s *ProxyService) SetAddr(ctx context.Context, req *proxypb.SetAddrRequest) (*proxypb.SetAddrResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config, ok := s.configs[name]
	if !ok {
		config = &ProxyConfig{}
		s.configs[name] = config
	}

	config.Addr = req.Addr

	if err := s.updateModuleConfig(name); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	s.log.Infow("successfully set address",
		zap.String("name", name),
		zap.String("addr", req.Addr),
	)

	return &proxypb.SetAddrResponse{}, nil
}

func ipToUint32(ip net.IP) uint32 {
	if len(ip) == 16 {
		return binary.LittleEndian.Uint32(ip[12:16])
	}
	return binary.LittleEndian.Uint32(ip)
}

func (s *ProxyService) updateModuleConfig(name string) error {
	moduleConfig, err := NewModuleConfig(s.agent, name)
	if err != nil {
		return fmt.Errorf("failed to create module config: %w", err)
	}

	config, ok := s.configs[name]
	if !ok {
		config = &ProxyConfig{}
		s.configs[name] = config
	}

	if err := moduleConfig.SetAddr(ipToUint32(net.ParseIP(config.Addr))); err != nil {
		return fmt.Errorf("failed to set addr: %w", err)
	}

	if err := s.agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	s.log.Debugw("successfully updated module config",
		zap.String("name", name),
	)

	return nil
}

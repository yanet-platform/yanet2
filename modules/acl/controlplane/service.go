package acl

import "C"
import (
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"go.uber.org/zap"
)

// ACLService реализует gRPC сервис для управления ACL
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	log     *zap.SugaredLogger
	configs map[instanceKey]*ACLConfig
}

// func NewACLService(agents []*ffi.Agent, log *zap.SugaredLogger) *ACLService {
// 	return &ACLService{
// 		agents:  agents,
// 		log:     log,
// 		configs: make(map[instanceKey]*ACLConfig),
// 	}
// }

type instanceKey struct {
	name     string
	instance uint32
}

type ACLConfig struct {
	Name   string
	Rules  []*aclpb.Rule
	Module *ModuleConfig
}

// func (s *ACLService) GetConfig(name string, instance uint32) (*ACLConfig, bool) {
// 	s.mu.RLock()
// 	defer s.mu.RUnlock()

// 	key := instanceKey{name: name, instance: instance}
// 	config, exists := s.configs[key]
// 	return config, exists
// }

// func (s *ACLService) UpdateConfig(ctx context.Context, req *aclpb.UpdateConfigRequest) (*aclpb.UpdateConfigResponse, error) {
// 	s.mu.Lock()
// 	defer s.mu.Unlock()

// 	name, instance, err := req.GetTarget().Validate(uint32(len(s.agents)))
// 	if err != nil {
// 		return nil, status.Error(codes.InvalidArgument, err.Error())
// 	}

// 	key := instanceKey{
// 		name:     name,
// 		instance: instance,
// 	}

// 	// освобождаем старый конфиг для этого инстанса
// 	if oldConfig, exists := s.configs[key]; exists && oldConfig.Module != nil {
// 		C.acl_module_config_free(oldConfig.Module.ptr)
// 	}

// 	// новый конфиг
// 	config := &ACLConfig{
// 		Name:  key.name,
// 		Rules: req.Rules,
// 	}

// 	// новый модуль для инстанса
// 	module, err := NewModuleConfig(s.agents[key.instance], key.name)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create module for instance %d: %w", key.instance, err)
// 	}

// 	// компилируем правила
// 	if err := s.compileRules(module, req.Rules); err != nil {
// 		return nil, fmt.Errorf("failed to compile rules for instance %d: %w", key.instance, err)
// 	}

// 	// обновляем модуль в dataplane
// 	if err := s.agents[key.instance].UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
// 		return nil, fmt.Errorf("failed to update module on instance %d: %w", key.instance, err)
// 	}

// 	config.Module = module
// 	s.configs[key] = config

// 	s.log.Infow("successfully updated ACL config",
// 		"name", key.name,
// 		"instance", key.instance,
// 		"rules", len(req.Rules),
// 	)

// 	return &aclpb.UpdateConfigResponse{}, nil
// }

// func (s *ACLService) compileRules(module *ModuleConfig, rules []*aclpb.Rule) error {
// 	if len(rules) == 0 {
// 		return nil
// 	}

// 	cRules := make([]C.filter_rule, len(rules))
// 	for i, rule := range rules {
// 		if err := s.convertProtoRuleToC(&cRules[i], rule); err != nil {
// 			return fmt.Errorf("rule %d: %w", i, err)
// 		}
// 	}

// 	rc, err := C.acl_module_compile(
// 		module.ptr,
// 		&cRules[0],
// 		C.uint32_t(len(cRules)),
// 	)
// 	if err != nil || rc != 0 {
// 		return fmt.Errorf("compilation failed: %w (code %d)", err, rc)
// 	}

// 	return nil
// }

// func (s *ACLService) convertProtoRuleToC(dst *C.filter_rule, src *aclpb.Rule) error {
// 	*dst = C.filter_rule{}

// 	if err := convertIPv6NetsToC(&dst.net6.srcs, &dst.net6.src_count, src.Filter.Src6S); err != nil {
// 		return fmt.Errorf("failed to convert IPv6 sources: %w", err)
// 	}
// 	if err := convertIPv6NetsToC(&dst.net6.dsts, &dst.net6.dst_count, src.Filter.Dst6S); err != nil {
// 		return fmt.Errorf("failed to convert IPv6 destinations: %w", err)
// 	}

// 	if err := convertIPv4NetsToC(&dst.net4.srcs, &dst.net4.src_count, src.Filter.Src4S); err != nil {
// 		return fmt.Errorf("failed to convert IPv4 sources: %w", err)
// 	}
// 	if err := convertIPv4NetsToC(&dst.net4.dsts, &dst.net4.dst_count, src.Filter.Dst4S); err != nil {
// 		return fmt.Errorf("failed to convert IPv4 destinations: %w", err)
// 	}

// 	if err := convertPortRangesToC(&dst.transport.srcs, &dst.transport.src_count, src.Filter.SrcPortRanges); err != nil {
// 		return fmt.Errorf("failed to convert source ports: %w", err)
// 	}
// 	if err := convertPortRangesToC(&dst.transport.dsts, &dst.transport.dst_count, src.Filter.DstPortRanges); err != nil {
// 		return fmt.Errorf("failed to convert destination ports: %w", err)
// 	}

// 	if err := convertProtoRangesToC(&dst.transport.protos, &dst.transport.proto_count, src.Filter.ProtoRanges); err != nil {
// 		return fmt.Errorf("failed to convert protocols: %w", err)
// 	}

// 	switch src.Action {
// 	case aclpb.ActionKind_ACTION_PASS:
// 		dst.action = C.uint32_t(1)
// 	case aclpb.ActionKind_ACTION_DENY:
// 		dst.action = C.uint32_t(2)
// 	case aclpb.ActionKind_ACTION_COUNT:
// 		dst.action = C.uint32_t(3) | C.ACTION_NON_TERMINATE
// 	case aclpb.ActionKind_ACTION_CHECK_STATE:
// 		dst.action = C.uint32_t(5) | C.ACTION_NON_TERMINATE
// 	default:
// 		return fmt.Errorf("unknown action kind: %v", src.Action)
// 	}

// 	if src.Log {
// 		dst.action |= C.ACTION_LOG_FLAG
// 	}

// 	if src.Filter.KeepState {
// 		dst.action |= C.ACTION_KEEP_STATE_FLAG
// 	}

// 	// никак не учитывается поле skip_to_count

// 	return nil
// }

// func convertIPv6NetsToC(cNet **C.net6, cCount *C.uint32_t, nets []*aclpb.IPNet) error {
// 	if len(nets) == 0 {
// 		*cNet = nil
// 		*cCount = 0
// 		return nil
// 	}

// 	cArray := (*C.net6)(C.malloc(C.size_t(len(nets)) * 16))
// 	if cArray == nil {
// 		return fmt.Errorf("memory allocation failed")
// 	}

// 	goSlice := unsafe.Slice(cArray, len(nets))

// 	for i, net := range nets {
// 		if len(net.Ip) != 16 {
// 			C.free(unsafe.Pointer(cArray))
// 			return fmt.Errorf("invalid IPv6 address length: %d", len(net.Ip))
// 		}
// 		copy(goSlice[i].addr[:], net.Ip)
// 		mask := netmask6(net.PrefixLen)
// 		copy(goSlice[i].mask[:], mask)
// 	}

// 	*cNet = cArray
// 	*cCount = C.uint32_t(len(nets))
// 	return nil
// }

// func convertIPv4NetsToC(cNet **C.net4, cCount *C.uint32_t, nets []*aclpb.IPNet) error {
// 	if len(nets) == 0 {
// 		*cNet = nil
// 		*cCount = 0
// 		return nil
// 	}

// 	cArray := (*C.net4)(C.malloc(C.size_t(len(nets)) * 4))
// 	if cArray == nil {
// 		return fmt.Errorf("memory allocation failed")
// 	}

// 	goSlice := unsafe.Slice(cArray, len(nets))

// 	for i, net := range nets {
// 		if len(net.Ip) != 4 {
// 			C.free(unsafe.Pointer(cArray))
// 			return fmt.Errorf("invalid IPv4 address length: %d", len(net.Ip))
// 		}
// 		copy(goSlice[i].addr[:], net.Ip)
// 		mask := netmask4(net.PrefixLen)
// 		copy(goSlice[i].mask[:], mask)
// 	}

// 	*cNet = cArray
// 	*cCount = C.uint32_t(len(nets))
// 	return nil
// }

// func convertPortRangesToC(cRanges **C.filter_port_range, cCount *C.uint16_t, ranges []*aclpb.PortRange) error {
// 	if len(ranges) == 0 {
// 		*cRanges = nil
// 		*cCount = 0
// 		return nil
// 	}

// 	cArray := (*C.filter_port_range)(C.malloc(C.size_t(len(ranges)) * 4))
// 	if cArray == nil {
// 		return fmt.Errorf("memory allocation failed")
// 	}

// 	goSlice := unsafe.Slice(cArray, len(ranges))

// 	for i, r := range ranges {
// 		goSlice[i].from = C.uint16_t(r.From)
// 		goSlice[i].to = C.uint16_t(r.To)
// 	}

// 	*cRanges = cArray
// 	*cCount = C.uint16_t(len(ranges))
// 	return nil
// }

// func convertProtoRangesToC(cRanges **C.filter_proto_range, cCount *C.uint16_t, ranges []*aclpb.ProtoRange) error {
// 	if len(ranges) == 0 {
// 		*cRanges = nil
// 		*cCount = 0
// 		return nil
// 	}

// 	cArray := (*C.filter_proto_range)(C.malloc(C.size_t(len(ranges)) * 4))
// 	if cArray == nil {
// 		return fmt.Errorf("memory allocation failed")
// 	}

// 	goSlice := unsafe.Slice(cArray, len(ranges))

// 	for i, r := range ranges {
// 		goSlice[i].from = C.uint16_t(r.From)
// 		goSlice[i].to = C.uint16_t(r.To)
// 	}

// 	*cRanges = cArray
// 	*cCount = C.uint16_t(len(ranges))
// 	return nil
// }

// func netmask6(prefixLen uint32) []byte {
// 	mask := make([]byte, 16)
// 	for i := uint32(0); i < 16; i++ {
// 		if prefixLen >= 8 {
// 			mask[i] = 0xff
// 			prefixLen -= 8
// 		} else if prefixLen > 0 {
// 			mask[i] = ^byte(0xff >> prefixLen)
// 			prefixLen = 0
// 		} else {
// 			mask[i] = 0
// 		}
// 	}
// 	return mask
// }

// func netmask4(prefixLen uint32) []byte {
// 	mask := make([]byte, 4)
// 	for i := uint32(0); i < 4; i++ {
// 		if prefixLen >= 8 {
// 			mask[i] = 0xff
// 			prefixLen -= 8
// 		} else if prefixLen > 0 {
// 			mask[i] = ^byte(0xff >> prefixLen)
// 			prefixLen = 0
// 		} else {
// 			mask[i] = 0
// 		}
// 	}
// 	return mask
// }

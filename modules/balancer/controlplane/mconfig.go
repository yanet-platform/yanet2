package mbalancer

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type ConfigRealInfo struct {
	Weight  uint16
	DstAddr netip.Addr
	SrcAddr netip.Addr
	SrcMask netip.Addr
	Enabled bool
	Stats   RealStats
}

func (info *ConfigRealInfo) IntoProto() *balancerpb.ConfigRealInfo {
	stats := info.Stats.IntoProto()
	return &balancerpb.ConfigRealInfo{
		Weight:  uint32(info.Weight),
		DstAddr: info.DstAddr.AsSlice(),
		SrcAddr: info.SrcAddr.AsSlice(),
		SrcMask: info.SrcMask.AsSlice(),
		Enabled: info.Enabled,
		Stats:   &stats,
	}
}

type ConfigVsInfo struct {
	Address    netip.Addr
	Port       uint16
	Proto      TransportProto
	AllowedSrc []netip.Prefix
	Reals      []ConfigRealInfo
	Flags      VsFlags
	Scheduler  VsScheduler
	Stats      VsStats
}

func (info *ConfigVsInfo) IntoProto() *balancerpb.ConfigVsInfo {
	allowedSrc := make([]*balancerpb.Subnet, 0, len(info.AllowedSrc))
	for idx := range info.AllowedSrc {
		src := info.AllowedSrc[idx]
		allowedSrc = append(allowedSrc, &balancerpb.Subnet{
			Addr: src.Addr().AsSlice(),
			Size: uint32(src.Bits()),
		})
	}

	reals := make([]*balancerpb.ConfigRealInfo, 0, len(info.Reals))
	for idx := range info.Reals {
		real := info.Reals[idx].IntoProto()
		reals = append(reals, real)
	}

	flags := info.Flags.IntoProto()

	stats := info.Stats.IntoProto()

	return &balancerpb.ConfigVsInfo{
		Ip:          info.Address.AsSlice(),
		Port:        uint32(info.Port),
		Proto:       info.Proto.IntoProto(),
		AllowedSrcs: allowedSrc,
		Reals:       reals,
		Flags:       &flags,
		Stats:       &stats,
	}
}

type ConfigInfo struct {
	Vs []ConfigVsInfo
}

func (info *ConfigInfo) IntoProto() *balancerpb.ConfigInfo {
	vsInfo := make([]*balancerpb.ConfigVsInfo, 0, len(info.Vs))
	for idx := range info.Vs {
		vs := &info.Vs[idx]
		vsInfo = append(vsInfo, vs.IntoProto())
	}
	return &balancerpb.ConfigInfo{
		VsInfo: vsInfo,
	}
}

func (configInfo *ConfigInfo) Json() string {
	if b, err := json.Marshal(configInfo); err != nil {
		return ""
	} else {
		return string(b)
	}
}

func (configInfo *ConfigInfo) JsonPretty() string {
	if b, err := json.MarshalIndent(configInfo, "", "  "); err != nil {
		return ""
	} else {
		return string(b)
	}
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleInstanceConfig) FindReal(
	vip *netip.Addr,
	realIp *netip.Addr,
	port uint16,
) *RealConfig {
	for serviceIdx := range config.Services {
		service := &config.Services[serviceIdx]
		if service.Info.Address == *vip &&
			(port == service.Info.Port || (service.Info.Flags.PureL3 && port == 0)) {
			for idx := range service.Reals {
				real := &service.Reals[idx]
				if real.DstAddr == *realIp {
					return real
				}
			}
		}
	}
	return nil
}

func (config *ModuleInstanceConfig) ValidateRealUpdate(
	update *RealUpdate,
) error {
	if real := config.FindReal(&update.VirtualIp, &update.RealIp, update.Port); real == nil {
		return fmt.Errorf(
			"real with address %s not found on virtual service %s:%d",
			update.RealIp,
			update.VirtualIp,
			update.Port,
		)
	} else {
		return nil
	}
}

func (config *ModuleInstanceConfig) UpdateReal(update *RealUpdate) error {
	real := config.FindReal(&update.VirtualIp, &update.RealIp, update.Port)
	if real == nil {
		return fmt.Errorf("failed to find real")
	}
	real.Enabled = update.Enable
	if update.Weight != 0 {
		real.Weight = uint16(update.Weight)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

func vsStatsFromCounters(counters [][]uint64) VsStats {
	for instance := 1; instance < len(counters); instance += 1 {
		for k := range counters[instance] {
			counters[0][k] += counters[instance][k]
		}
	}
	return VsStats{
		IncomingPackets:      counters[0][0],
		IncomingBytes:        counters[0][1],
		PacketSrcNotAllowed:  counters[0][2],
		NoReals:              counters[0][3],
		OpsPackets:           counters[0][4],
		SessionTableOverflow: counters[0][5],
		RealIsDisabled:       counters[0][6],
		PacketNotRescheduled: counters[0][7],
		CreatedSessions:      counters[0][8],
		OutgoingPackets:      counters[0][9],
		OutgoingBytes:        counters[0][10],
	}
}

func realStatsFromCounters(counters [][]uint64) RealStats {
	for instance := 1; instance < len(counters); instance += 1 {
		for k := range counters[instance] {
			counters[0][k] += counters[instance][k]
		}
	}
	return RealStats{
		RealDisabledPackets: counters[0][0],
		OpsPackets:          counters[0][1],
		CreatedSessions:     counters[0][2],
		SendPackets:         counters[0][3],
		SendBytes:           counters[0][4],
	}
}

////////////////////////////////////////////////////////////////////////////////

func findVsCounters(vs *VirtualService, counters []ffi.CounterInfo) *ffi.CounterInfo {
	for idx := range counters {
		counter := &counters[idx]
		if counter.Name == fmt.Sprintf("v%d", vs.RegistryIdx) {
			// found
			return counter
		}
	}
	return nil
}

func findRealCounters(real *Real, counters []ffi.CounterInfo) *ffi.CounterInfo {
	for idx := range counters {
		counter := &counters[idx]
		if counter.Name == fmt.Sprintf("r%d", real.RegistryIdx) {
			// found
			return counter
		}
	}
	return nil
}

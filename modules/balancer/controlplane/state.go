package mbalancer

import (
	"encoding/json"
	"net"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

////////////////////////////////////////////////////////////////////////////////

type ipJson []byte

func (ip ipJson) MarshalJSON() ([]byte, error) {
	s := net.IP(ip).String()
	return json.Marshal(s)
}

////////////////////////////////////////////////////////////////////////////////

type StateRealInfo struct {
	Vip                 ipJson
	VirtualPort         uint16
	RealIp              ipJson
	TransportProto      TransportProto
	ActiveSessions      uint64
	LastPacketTimestamp time.Time
	Stats               RealStats
}

func (info *StateRealInfo) IntoProto() *balancerpb.StateRealInfo {
	stats := info.Stats.IntoProto()
	return &balancerpb.StateRealInfo{
		Vip:                 info.Vip,
		VirtualPort:         uint32(info.VirtualPort),
		RealIp:              info.RealIp,
		VsProto:             info.TransportProto.IntoProto(),
		ActiveSessions:      info.ActiveSessions,
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
		Stats:               &stats,
	}
}

type StateVsInfo struct {
	Ip                  ipJson
	Port                uint16
	TransportProto      TransportProto
	ActiveSessions      uint64
	LastPacketTimestamp time.Time
	Stats               VsStats
}

func (info *StateVsInfo) IntoProto() *balancerpb.StateVsInfo {
	stats := info.Stats.IntoProto()
	return &balancerpb.StateVsInfo{
		Ip:                  info.Ip,
		Port:                uint32(info.Port),
		TransportProto:      info.TransportProto.IntoProto(),
		ActiveSessions:      info.ActiveSessions,
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
		Stats:               &stats,
	}
}

type StateInfo struct {
	VsInfo   []StateVsInfo
	RealInfo []StateRealInfo
}

func (info *StateInfo) IntoProto() *balancerpb.StateInfo {
	infoPb := balancerpb.StateInfo{
		Vs:   make([]*balancerpb.StateVsInfo, 0, len(info.VsInfo)),
		Real: make([]*balancerpb.StateRealInfo, 0, len(info.RealInfo)),
	}
	for idx := range info.VsInfo {
		vs := &info.VsInfo[idx]
		infoPb.Vs = append(infoPb.Vs, vs.IntoProto())
	}
	for idx := range info.RealInfo {
		real := &info.RealInfo[idx]
		infoPb.Real = append(infoPb.Real, real.IntoProto())
	}
	return &infoPb
}

////////////////////////////////////////////////////////////////////////////////

func (stateInfo *StateInfo) Json() string {
	if b, err := json.Marshal(stateInfo); err != nil {
		return ""
	} else {
		return string(b)
	}
}

// useful for debug
func (stateInfo *StateInfo) JsonPretty() string {
	if b, err := json.MarshalIndent(stateInfo, "", "  "); err != nil {
		return ""
	} else {
		return string(b)
	}
}

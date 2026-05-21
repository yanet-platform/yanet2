package fwstate

//#include "lib/fwstate/config.h"
import "C"

import (
	"encoding/binary"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

func htons(v uint16) uint16 {
	var beu16 [2]byte
	binary.BigEndian.PutUint16(beu16[:], v)
	// beu16 contains v in big-endian byte order.
	return uint16(beu16[1])<<8 | uint16(beu16[0])
}

func ntohs(v uint16) uint16 {
	var beu16 [2]byte
	// Split v into bytes: low byte goes to index 0, high byte to index 1.
	beu16[0] = uint8(v)
	beu16[1] = uint8(v >> 8)

	// Read bytes as big-endian uint16 to convert from network to host order.
	return binary.BigEndian.Uint16(beu16[:])
}

// ConvertPbToCSyncConfig converts protobuf SyncConfig directly to C struct
func ConvertPbToCSyncConfig(pb *fwstatepb.SyncConfig) C.struct_fwstate_sync_config {
	var cSyncConfig C.struct_fwstate_sync_config

	copy(unsafe.Slice((*byte)(&cSyncConfig.src_addr[0]), 16), pb.GetSrcAddr().GetAddr())
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_ether)), 6), pb.DstEther)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_addr_multicast[0])), 16), pb.GetDstAddrMulticast().GetAddr())
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_addr_unicast[0])), 16), pb.GetDstAddrUnicast().GetAddr())

	// Copy ports - convert to network bytes order for direct comparisons in the dataplane.
	cSyncConfig.port_multicast = C.uint16_t(htons(uint16(pb.GetPortMulticast())))
	cSyncConfig.port_unicast = C.uint16_t(htons(uint16(pb.GetPortUnicast())))

	// Copy timeouts
	cSyncConfig.timeouts.tcp_syn_ack = C.uint64_t(pb.GetTcpSynAck())
	cSyncConfig.timeouts.tcp_syn = C.uint64_t(pb.GetTcpSyn())
	cSyncConfig.timeouts.tcp_fin = C.uint64_t(pb.GetTcpFin())
	cSyncConfig.timeouts.tcp = C.uint64_t(pb.GetTcp())
	cSyncConfig.timeouts.udp = C.uint64_t(pb.GetUdp())
	cSyncConfig.timeouts.default_ = C.uint64_t(pb.GetDefault())

	return cSyncConfig
}

// ConvertCSyncConfigToPb converts C struct to protobuf SyncConfig
func ConvertCSyncConfigToPb(cCfg *C.struct_fwstate_sync_config) *fwstatepb.SyncConfig {
	srcAddr := make([]byte, 16)
	copy(srcAddr, unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.src_addr[0])), 16))

	dstEther := make([]byte, 6)
	copy(dstEther, unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_ether)), 6))

	dstAddrMulticast := make([]byte, 16)
	copy(dstAddrMulticast, unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_addr_multicast[0])), 16))

	dstAddrUnicast := make([]byte, 16)
	copy(dstAddrUnicast, unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_addr_unicast[0])), 16))

	return &fwstatepb.SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: srcAddr},
		DstEther:         dstEther,
		DstAddrMulticast: &commonpb.IPAddress{Addr: dstAddrMulticast},
		DstAddrUnicast:   &commonpb.IPAddress{Addr: dstAddrUnicast},
		PortMulticast:    uint32(ntohs(uint16(cCfg.port_multicast))),
		PortUnicast:      uint32(ntohs(uint16(cCfg.port_unicast))),
		TcpSynAck:        uint64(cCfg.timeouts.tcp_syn_ack),
		TcpSyn:           uint64(cCfg.timeouts.tcp_syn),
		TcpFin:           uint64(cCfg.timeouts.tcp_fin),
		Tcp:              uint64(cCfg.timeouts.tcp),
		Udp:              uint64(cCfg.timeouts.udp),
		Default:          uint64(cCfg.timeouts.default_),
	}
}

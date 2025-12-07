package ffi

// #include <stdlib.h>
// #include <string.h>
// #include <stdint.h>
// #include <netinet/in.h>
// #include <stdlib.h>
import "C"
import (
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

func sliceToPtr(s []byte) *C.uint8_t {
	return (*C.uint8_t)(&s[0])
}

func ptrToSlice(p *C.uint8_t, len int) []byte {
	return C.GoBytes(unsafe.Pointer(p), C.int(len))
}

func addressToSlice(p *C.uint8_t, addr C.int) []byte {
	len := 16
	if addr == C.IPPROTO_IP {
		len = 4
	}
	return ptrToSlice(p, len)
}

func addrToIpProto(addr *netip.Addr) C.int {
	if addr.Is4() {
		return C.IPPROTO_IP
	} else {
		return C.IPPROTO_IPV6
	}
}

func transportProtoToIpProto(proto module.Proto) C.int {
	if proto == module.ProtoTcp {
		return C.IPPROTO_TCP
	} else {
		return C.IPPROTO_UDP
	}
}

func ipFromC(ptr *C.uint8_t, ipProto C.int) netip.Addr {
	b := addressToSlice(ptr, ipProto)
	if addr, ok := netip.AddrFromSlice(b); ok {
		return addr
	}
	return netip.Addr{} // invalid
}

func moduleProtoFromC(p C.int) module.Proto {
	if p == C.IPPROTO_TCP {
		return module.ProtoTcp
	}
	return module.ProtoUdp
}

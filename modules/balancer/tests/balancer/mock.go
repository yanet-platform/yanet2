package test_balancer

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../../ -I../../../../../../lib -I../../../../../common
//#cgo LDFLAGS: -L../../../../build/tests/utils -lyanet_test_utils
//#cgo LDFLAGS: -L../../../../build/modules/balancer/tests/utils -lbalancer_test_utils
//#cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/modules/balancer/dataplane -lbalancer_dp
//#cgo LDFLAGS: -L../../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

struct dp_worker;
struct module_ectx;
struct packet_front;

void
balancer_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

*/
import "C"
import (
	"github.com/gopacket/gopacket"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	test_utils "github.com/yanet-platform/yanet2/tests/utils/go"
)

func HandlePackets(
	instance *balancer.ModuleInstance,
	mock *test_utils.YanetMock,
	packets ...gopacket.Packet,
) (test_utils.HandlePacketsResult, error) {
	cpModule := instance.ModuleConfig().AsRawPtr()
	return mock.HandlePackets(cpModule, C.balancer_handle_packets, packets...)
}

package balancer

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../../ -I../../../../../../lib -I../../../../../common
//#cgo LDFLAGS: -L../../../../build/test_utils -lyanet_test_utils
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
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	test_utils "github.com/yanet-platform/yanet2/test_utils/go"
)

var (
	dpMemory    uint64 = 1 << 20
	agentMemory uint64 = 1 << 28
	cpMemory    uint64 = 2*agentMemory + (1 << 28) // must be at least 2x agentMemory
)

var mock *test_utils.YanetMock

func HandlePackets(
	instance *balancer.ModuleInstance,
	packets ...gopacket.Packet,
) (test_utils.HandlePacketsResult, error) {
	cpModule := instance.ModuleConfig().AsRawPtr()
	return mock.HandlePackets(cpModule, C.balancer_handle_packets, packets...)
}

func AttachAgent(t *testing.T) *ffi.Agent {
	t.Helper()
	agent, err := mock.AttachAgent("balancer", agentMemory)
	if err != nil {
		t.Fatalf("failed to attach agent to yanet mock: %v", err)
	}
	return agent
}

func PrepareForUpdate(t *testing.T) {
	if err := mock.PrepareForCpUpdate(); err != nil {
		t.Fatalf("failed to prepare for controlplane update: %v", err)
	}
}

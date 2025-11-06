package test_acl

//#cgo LDFLAGS: -L../../../../build/modules/acl/dataplane -lacl_dp
//#cgo LDFLAGS: -L../../../../build/tests/utils -lyanet_test_utils
//
//struct packet_front;
//struct dp_worker;
//struct module_ectx;
//
//void
//acl_handle_packets(
//	struct dp_worker *dp_worker,
//	struct module_ectx *module_ectx,
//	struct packet_front *packet_front
//);
//
import "C"

import (
	"github.com/gopacket/gopacket"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	test_utils "github.com/yanet-platform/yanet2/tests/utils/go"
)

func HandlePackets(
	mock *test_utils.YanetMock,
	acl *acl.ModuleConfig,
	packets ...gopacket.Packet,
) (test_utils.HandlePacketsResult, error) {
	return mock.HandlePackets(acl.AsRawPtr(), C.acl_handle_packets, packets...)
}

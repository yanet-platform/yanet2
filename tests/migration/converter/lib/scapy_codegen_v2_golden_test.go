package lib

import (
	"strings"
	"testing"
)

func TestCodegen_Golden_SimpleIPv4UDP(t *testing.T) {
	ir := IRJSON{
		PCAPPairs: []IRPCAPPair{
			{
				SendFile:   "001-send.pcap",
				ExpectFile: "001-expect.pcap",
				SendPackets: []IRPacketDef{
					{
						Layers: []IRLayer{
							{Type: "Ether", Params: map[string]interface{}{}},
							{Type: "IP", Params: map[string]interface{}{
								"src": "192.0.2.10", "dst": "198.51.100.1", "ttl": 64, "proto": 17,
							}},
							{Type: "UDP", Params: map[string]interface{}{
								"sport": 12345, "dport": 53,
							}},
							{Type: "Raw", Params: map[string]interface{}{"_arg0": "payload"}},
						},
					},
				},
			},
		},
	}

	irJSON, err := ir.ToJSON()
	if err != nil {
		t.Fatalf("failed to marshal IR: %v", err)
	}

	cg := NewScapyCodegenV2(false)
	code, err := cg.GenerateFromIR(irJSON)
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}

	// Golden-style assertions (substring presence to keep it robust)
	wantSubs := []string{
		"package converted",
		"func Generate001_send_pcapSend(",
		"lib.Ether(",
		"lib.IP(",
		"lib.UDPSport(",
		"lib.UDPDport(",
		"lib.Raw(",
	}
	for _, sub := range wantSubs {
		if !strings.Contains(code, sub) {
			t.Fatalf("generated code missing expected fragment: %q", sub)
		}
	}
}



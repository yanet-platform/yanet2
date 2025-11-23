package lib

import (
	"encoding/json"
)

// IRLayer represents a single protocol or payload layer in the intermediate
// representation passed between the AST/PCAP analyzers and the Go code
// generator.
//
// Invariants:
//   - Type matches a concrete builder in packet_builder.go (e.g. "Ether",
//     "Dot1Q", "IPv4", "IPv6", "TCP", "UDP", "ICMP", "GRE", "MPLS", "ARP",
//     "IPSecESP", "IPSecAH", "IPv6ExtHdrFragment", "Raw", ...).
//   - Params contains only JSON-serializable values (string, float64, bool,
//     []interface{}, map[string]interface{}).
//   - For malformed packets we prefer to preserve raw lengths/checksums/options
//     exactly as seen in the original PCAP, even if they are inconsistent.
type IRLayer struct {
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

// IRPacketDef represents one logical packet reconstructed from a pair of
// Scapy definitions or directly from a PCAP. Layers appear in on-the-wire
// order starting from Ethernet.
//
// SpecialHandling is an optional bag for flags such as fragmentation,
// GRE flags, or other features that the generator cannot easily infer
// from individual layers alone.
type IRPacketDef struct {
	Layers          []IRLayer              `json:"layers"`
	SpecialHandling map[string]interface{} `json:"special_handling"`
}

// IRPCAPPair represents a pair of send/expect PCAP files and the reconstructed
// packets for each side. For drop tests the ExpectPackets slice may be empty.
type IRPCAPPair struct {
	SendFile      string        `json:"send_file"`
	ExpectFile    string        `json:"expect_file"`
	SendPackets   []IRPacketDef `json:"send_packets"`
	ExpectPackets []IRPacketDef `json:"expect_packets"`
}

// IRJSON represents the complete intermediate representation produced by either
// the Python AST parser or the Go PCAP analyzer.
//
// Contracts:
//   - PCAPPairs is non-empty for a valid test conversion.
//   - HelperFunctions lists extra Go helpers that must be emitted alongside the
//     generated test (e.g. fragmentation helpers or custom GRE builders).
type IRJSON struct {
	PCAPPairs       []IRPCAPPair `json:"pcap_pairs"`
	HelperFunctions []string     `json:"helper_functions"`
}

// ToJSON converts IRJSON to JSON string
func (ir *IRJSON) ToJSON() (string, error) {
	jsonBytes, err := json.Marshal(ir)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}



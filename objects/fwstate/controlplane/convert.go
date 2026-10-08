package fwstatemap

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// fromCursorKey returns a protobuf key with owned address copies.
func fromCursorKey(key cfwstate.StateKey) *fwstatemappb.FwStateKey {
	return &fwstatemappb.FwStateKey{
		Proto:   key.Proto,
		SrcPort: key.SrcPort,
		DstPort: key.DstPort,
		SrcAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.SrcAddr...)},
		DstAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.DstAddr...)},
	}
}

// fromCursorValue preserves state flags, timestamps and directional counters.
func fromCursorValue(value cfwstate.StateValue) *fwstatemappb.FwStateValue {
	return &fwstatemappb.FwStateValue{
		External:        value.External,
		Flags:           value.Flags,
		CreatedAt:       value.CreatedAt,
		UpdatedAt:       value.UpdatedAt,
		PacketsBackward: value.PacketsBackward,
		PacketsForward:  value.PacketsForward,
	}
}

// fromCursorEntry retains cursor position and expiry alongside state data.
func fromCursorEntry(entry cfwstate.CursorEntry) *fwstatemappb.FwStateEntry {
	return &fwstatemappb.FwStateEntry{
		Key:     fromCursorKey(entry.Key),
		Value:   fromCursorValue(entry.Value),
		Idx:     entry.Idx,
		Expired: entry.Expired,
	}
}

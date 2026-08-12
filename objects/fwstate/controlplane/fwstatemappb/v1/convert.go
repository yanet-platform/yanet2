package fwstatemappb

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// FromCursorKey converts a cfwstate cursor key into its proto form.
func FromCursorKey(key cfwstate.StateKey) *FwStateKey {
	return &FwStateKey{
		Proto:   key.Proto,
		SrcPort: key.SrcPort,
		DstPort: key.DstPort,
		SrcAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.SrcAddr...)},
		DstAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.DstAddr...)},
	}
}

// FromCursorValue converts a cfwstate cursor value into its proto form.
func FromCursorValue(value cfwstate.StateValue) *FwStateValue {
	return &FwStateValue{
		External:        value.External,
		Flags:           value.Flags,
		CreatedAt:       value.CreatedAt,
		UpdatedAt:       value.UpdatedAt,
		PacketsBackward: value.PacketsBackward,
		PacketsForward:  value.PacketsForward,
	}
}

// FromCursorEntry converts a cfwstate cursor entry into its proto form.
func FromCursorEntry(entry cfwstate.CursorEntry) *FwStateEntry {
	return &FwStateEntry{
		Key:     FromCursorKey(entry.Key),
		Value:   FromCursorValue(entry.Value),
		Idx:     entry.Idx,
		Expired: entry.Expired,
	}
}

//go:build linux && (amd64 || arm64)

package lpm

import "github.com/yanet-platform/yanet2/controlplane/native/layout"

// Walk calls visit with every stretch of keys the tree maps to one
// value, in ascending key order, one per run of neighbouring slots of a
// page that hold one value.
//
// The first and last key of a stretch come as slices of keySize bytes,
// valid only during the call; keySize is the size of the tree's keys.
// Keys no insert mapped come with the invalid value the lookup returns
// for them.
func Walk(tree *layout.Lpm, keySize int, visit func(from, to []byte, value uint32)) {
	from := make([]byte, keySize)
	to := make([]byte, keySize)
	walkPage(page(tree, 0), 0, from, to, visit)
}

// walkPage visits the stretches under one page, the key bytes before its
// hop already set in both keys.
func walkPage(current *layout.LpmPage, hop int, from, to []byte, visit func(from, to []byte, value uint32)) {
	for key := 0; key < len(current.Values); key++ {
		slot := &current.Values[key]
		from[hop] = byte(key)
		value := *slotValue(slot)
		if value&valueFlag == 0 {
			to[hop] = byte(key)
			walkPage(slotPage(slot).Get(), hop+1, from, to, visit)
			continue
		}
		for key+1 < len(current.Values) && *slotValue(&current.Values[key+1]) == value {
			key++
		}
		to[hop] = byte(key)
		for rest := hop + 1; rest < len(from); rest++ {
			from[rest] = 0
			to[rest] = 0xff
		}
		visit(from, to, uint32(value>>1))
	}
}

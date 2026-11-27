//go:build linux && (amd64 || arm64)

package classify

// hashIndex finds numbered items by their hashes, as C's join index
// does: an open addressing table of item numbers plus one, zero marking
// a free slot, probed linearly from the hash and kept at most half full.
//
// The caller keeps the items and tells equal ones apart. Items under one
// hash lie on its probe path in the order they were added, so a lookup
// finds the first of equal ones.
type hashIndex struct {
	// Hashes holds the hash of every item, by its number.
	Hashes []uint32
	// Slots holds item numbers plus one; its size is a power of two.
	Slots []uint32
}

// newHashIndex returns an empty index.
func newHashIndex() hashIndex {
	return hashIndex{Slots: make([]uint32, 16)}
}

// Find returns the number of the first item added under a hash that
// equal accepts, or none.
func (m *hashIndex) Find(hash uint32, equal func(number uint32) bool) uint32 {
	mask := uint32(len(m.Slots) - 1)
	for slot := hash & mask; m.Slots[slot] != 0; slot = (slot + 1) & mask {
		if number := m.Slots[slot] - 1; m.Hashes[number] == hash && equal(number) {
			return number
		}
	}
	return none
}

// Add numbers the next item under a hash and returns its number; the
// slots double, every item placed anew in its order, once they would
// be over half full.
func (m *hashIndex) Add(hash uint32) uint32 {
	number := uint32(len(m.Hashes))
	m.Hashes = append(m.Hashes, hash)
	if 2*len(m.Hashes) <= len(m.Slots) {
		m.place(number, hash)
		return number
	}
	m.Slots = make([]uint32, 2*len(m.Slots))
	for item, itemHash := range m.Hashes {
		m.place(uint32(item), itemHash)
	}
	return number
}

// place puts an item at the first free slot of its probe path.
func (m *hashIndex) place(number, hash uint32) {
	mask := uint32(len(m.Slots) - 1)
	slot := hash & mask
	for m.Slots[slot] != 0 {
		slot = (slot + 1) & mask
	}
	m.Slots[slot] = number + 1
}

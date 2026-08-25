#pragma once

/*
 * Direct-mapped result memo for the net6 classifier.
 *
 * The classifier resolves an IPv6 address by walking two byte-stride tries
 * (hi and lo halves) and fetching a combine-table cell; real traffic repeats
 * addresses heavily (measured 81..90% repeat on a production capture), so the
 * final verdict is memoized on the full 16-byte address. A hit is one
 * aligned 16-byte load replacing both walks and the combine fetch.
 *
 * The memo lives inside struct net6_classifier and is allocated from the
 * filter's shared-memory context at compile time, zeroed. A config swap
 * installs a whole new classifier, so the memo is generation-scoped by
 * construction: no invalidation protocol, and stale entries are impossible.
 *
 * Entry encoding, one unsigned __int128 per slot:
 *   bit 127   valid (a zero entry means empty, and the valid bit is forced
 *             into every stored entry so a real entry never reads empty)
 *   bits 96..126  verdict (combine value; these are registry indices far
 *             below 2^31)
 *   bits 0..95   96-bit key: a splitmix64 mix of the two address words
 *             xor-extended with crc32c of the full 16 bytes. A false hit
 *             requires two distinct addresses whose 96-bit keys collide AND
 *             which land in the same slot: about 2^-96 per pair, and the
 *             slot index already derives from an independent hash.
 *
 * Multi-writer safety without locked instructions: a slot is two naturally
 * aligned 8-byte words. low holds the key's low 64 bits; high holds the
 * key's upper 32 bits, the verdict, and the valid bit. Writers store low
 * first, then high; readers load high first, then low. A reader that
 * catches a slot mid-overwrite sees either an old high word (misses on the
 * valid bit or the key), or a new high word whose low-word mismatch fails
 * the full 96-bit key compare — a miss, never a wrong verdict, because a
 * passing key compare identifies the address and the verdict is a pure
 * function of the address within one classifier generation. All accesses
 * are plain naturally aligned 8-byte loads and stores, atomic on x86-64
 * TSO with the stated ordering.
 *
 * A slot entry is laid out across two words rather than one 16-byte atomic
 * (cmpxchg16b) deliberately: the locked compare-and-swap cost more than
 * the memo saves (measured on the pcap replay benchmark).
 */

#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#include "common/crc32.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"

// Memo capacities tried at init, largest first: all powers of two so the
// slot index is hash & (N - 1).
//
// 32768 slots hold the distinct addresses of the production capture
// (9.2k src, 16.7k dst) without direct-mapped thrashing, at 512 KiB per
// classifier. Denser control planes that juggle many module configs in
// one agent arena fall back to the smaller sizes rather than failing the
// compile; an arena too small for the smallest table disables the memo
// entirely (always-miss, the plain walks serve every lookup).
#define NET6_MEMO_SLOTS_MAX 32768
#define NET6_MEMO_SLOTS_MID 8192
#define NET6_MEMO_SLOTS_MIN 2048

// Valid bit, bit 0 of the high word: a zero high word is an empty slot and
// every stored entry carries the bit.
#define NET6_MEMO_VALID_U64 1ull

struct net6_memo_slot {
	uint64_t low;
	uint64_t high;
};

struct net6_memo {
	// Shared-memory relative pointer to the slot table.
	struct net6_memo_slot *slots;
	// Capacity actually allocated (power of two); 0 with slots == NULL
	// when the memo is disabled.
	uint32_t slots_count;
};

// Pick the memo capacity for one arena: the largest power-of-two table
// (between MIN and MAX slots) whose bytes fit an eighth of the arena's
// currently free space, or 0 to disable the memo.
//
// A dense control plane can hold many classifiers in one agent arena, and
// a fixed half-megabyte per memo would turn an almost-full arena into a
// compile failure. Sizing against the remaining free space before any
// allocation keeps the memo proportional to what the arena can spare, so
// the compile itself never fails on the memo; a disabled memo (capacity
// 0, slots NULL) means always-miss and the plain walks serve every
// lookup.
static inline uint32_t
net6_memo_capacity(struct memory_context *memory_context) {
	struct block_allocator *allocator =
		ADDR_OF(&memory_context->block_allocator);
	size_t budget = block_allocator_free_size(allocator) / 8;

	uint32_t capacity = NET6_MEMO_SLOTS_MAX;
	while (capacity > NET6_MEMO_SLOTS_MIN &&
	       (size_t)capacity * sizeof(struct net6_memo_slot) > budget) {
		capacity /= 2;
	}
	if ((size_t)capacity * sizeof(struct net6_memo_slot) > budget) {
		return 0;
	}
	return capacity;
}

// Allocate and zero the slot table at the capacity picked by
// net6_memo_capacity.
//
// Returns 0 on success, -1 on allocation failure (slots stays NULL, and
// net6_memo_lookup treats a NULL table as always-miss, so the caller may
// also treat the failure as fatal without a query-path hazard).
static inline int
net6_memo_init(struct net6_memo *memo, struct memory_context *memory_context) {
	SET_OFFSET_OF(&memo->slots, NULL);
	memo->slots_count = 0;

	uint32_t capacity = net6_memo_capacity(memory_context);
	if (capacity == 0) {
		return 0;
	}

	struct net6_memo_slot *slots = (struct net6_memo_slot *)memory_balloc(
		memory_context, capacity * sizeof(*slots)
	);
	if (slots == NULL) {
		return -1;
	}
	memset(slots, 0, capacity * sizeof(*slots));
	SET_OFFSET_OF(&memo->slots, slots);
	memo->slots_count = capacity;
	return 0;
}

// Release the slot table. NULL-safe.
static inline void
net6_memo_fini(struct net6_memo *memo, struct memory_context *memory_context) {
	struct net6_memo_slot *slots = ADDR_OF(&memo->slots);
	if (slots == NULL) {
		return;
	}
	memory_bfree(memory_context, slots, memo->slots_count * sizeof(*slots));
	SET_OFFSET_OF(&memo->slots, NULL);
	memo->slots_count = 0;
}

// 96-bit key of one address: splitmix64 over the two 64-bit words, folded
// with crc32c over all 16 bytes.
static inline unsigned __int128
net6_memo_key(const uint8_t addr[16]) {
	uint64_t lo;
	uint64_t hi;
	memcpy(&lo, addr, 8);
	memcpy(&hi, addr + 8, 8);

	uint64_t mix = lo * 0x9e3779b97f4a7c15ULL ^ hi * 0xc2b2ae3d27d4eb4fULL;
	mix ^= mix >> 29;
	mix *= 0xbf58476d1ce4e5b9ULL;
	mix ^= mix >> 32;

	unsigned __int128 key = (unsigned __int128)mix;
	key |= (unsigned __int128)crc32(addr, 16, 0) << 64;
	return key;
}

// Look up addr; returns true and stores the verdict on a hit.
//
// A NULL table always misses. Loads the high word first: a zero high word
// is an empty slot, and the full 96-bit key compare guards torn reads per
// the two-word protocol in the header comment.
static inline bool
net6_memo_lookup(
	const struct net6_memo *memo, const uint8_t addr[16], uint32_t *verdict
) {
	const struct net6_memo_slot *slots = ADDR_OF(&memo->slots);
	if (slots == NULL) {
		return false;
	}

	uint32_t slot = crc32(addr, 16, 0) & (memo->slots_count - 1);
	uint64_t high = __atomic_load_n(&slots[slot].high, __ATOMIC_RELAXED);
	if (!(high & NET6_MEMO_VALID_U64)) {
		return false;
	}

	uint64_t low = __atomic_load_n(&slots[slot].low, __ATOMIC_RELAXED);

	unsigned __int128 key = net6_memo_key(addr);
	if (low != (uint64_t)key ||
	    (uint32_t)(high >> 32) != (uint32_t)(key >> 64)) {
		return false;
	}

	*verdict = (uint32_t)((high >> 1) & 0x7fffffffu);
	return true;
}

// Insert (addr, verdict). Overwrites whatever occupied the slot.
//
// Stores the low word first and the high word (carrying the valid bit)
// last; x86-64 TSO keeps the stores in program order, so a reader never
// observes a valid high word before its low word.
static inline void
net6_memo_insert(
	struct net6_memo *memo, const uint8_t addr[16], uint32_t verdict
) {
	struct net6_memo_slot *slots = ADDR_OF(&memo->slots);
	if (slots == NULL) {
		return;
	}

	unsigned __int128 key = net6_memo_key(addr);
	uint64_t high = ((uint64_t)(uint32_t)(key >> 64) << 32) |
			((uint64_t)(verdict & 0x7fffffffu) << 1) |
			NET6_MEMO_VALID_U64;

	uint32_t slot = crc32(addr, 16, 0) & (memo->slots_count - 1);
	__atomic_store_n(&slots[slot].low, (uint64_t)key, __ATOMIC_RELAXED);
	__atomic_store_n(&slots[slot].high, high, __ATOMIC_RELEASE);
}

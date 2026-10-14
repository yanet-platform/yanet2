#pragma once

#include "network.h"

#include <endian.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

// IPv4 prefix-length classification requires a contiguous mask.
//
// A contiguous prefix mask is a run of set high bits followed only by zero
// bits, which is the only shape the LPM-derived prefix length classifiers
// can represent correctly.
static inline bool
network_net4_mask_is_valid(const uint8_t mask[NET4_LEN]) {
	uint32_t bits;
	memcpy(&bits, mask, sizeof(bits));
	uint32_t inv = ~be32toh(bits);
	return (inv & (inv + 1)) == 0;
}

// IPv6 classification requires independently contiguous 64-bit mask halves.
//
// Bi-contiguous means each 64-bit half of the mask (bytes[0..7] and
// bytes[8..15]) is independently a contiguous prefix mask, which matches
// how the IPv6 classifiers derive a prefix length per half via popcount. A
// hole exactly at the /64 boundary is therefore still accepted; a hole
// within a half is not.
static inline bool
network_net6_mask_is_valid(const uint8_t mask[NET6_LEN]) {
	uint64_t hi, lo;
	memcpy(&hi, mask, sizeof(hi));
	memcpy(&lo, mask + 8, sizeof(lo));

	// Contiguous prefix masks have inverses consisting only of low set
	// bits.
	uint64_t hi_inv = ~be64toh(hi);
	uint64_t lo_inv = ~be64toh(lo);
	return (hi_inv & (hi_inv + 1)) == 0 && (lo_inv & (lo_inv + 1)) == 0;
}

// Invalid IPv4 masks require rejecting the containing configuration.
//
// Control-plane descriptors can carry raw masks that prefix-length
// classification cannot represent.
static inline bool
network_net4_masks_are_valid(const struct net4 *nets, uint32_t count) {
	for (uint32_t net_idx = 0; net_idx < count; ++net_idx) {
		if (!network_net4_mask_is_valid(nets[net_idx].mask)) {
			return false;
		}
	}
	return true;
}

// Invalid IPv6 mask halves require rejecting the containing configuration.
static inline bool
network_net6_masks_are_valid(const struct net6 *nets, uint32_t count) {
	for (uint32_t net_idx = 0; net_idx < count; ++net_idx) {
		if (!network_net6_mask_is_valid(nets[net_idx].mask)) {
			return false;
		}
	}
	return true;
}

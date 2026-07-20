#pragma once

// Toeplitz hash, the RSS hash function used by NIC drivers (mlx5, i40e, and
// most other RSS-capable devices) to steer incoming packets to a receive
// queue. This header is DPDK-free by design so it can be shared by code
// that must not depend on rte_* headers, so do not include rte_thash.h or
// call rte_softrss here.
//
// The implementation below is the classic MSB-first bit-walking Toeplitz
// hash: a 32-bit window is initialised from the first four key bytes, then
// shifted left one bit per input bit, pulling in one new key bit at a time —
// whenever an input bit is set the current window is XORed into the
// running hash. It is bit-identical to DPDK's rte_softrss_be given the
// same raw key bytes and the same input bytes.
//
// The caller is responsible for assembling the input tuple bytes in
// whatever order the NIC hashes them (typically source address,
// destination address, source port, destination port, but the exact
// byte/field order is driver- and configuration-dependent). This header
// does not attempt to guess that order — it must be verified empirically
// against a live capture by the caller.
//
// The caller must pass key_len >= 4, since the initial 32-bit window is
// seeded from the first four key bytes. Real RSS keys are at least 40
// bytes, so this always holds in practice.

#include <stdint.h>

static inline uint32_t
thash_toeplitz(
	const uint8_t *key,
	uint32_t key_len,
	const uint8_t *input,
	uint32_t input_len
) {
	uint32_t hash = 0;
	uint32_t window = ((uint32_t)key[0] << 24) | ((uint32_t)key[1] << 16) |
			  ((uint32_t)key[2] << 8) | (uint32_t)key[3];

	uint32_t input_bits = input_len * 8;
	uint32_t key_bits = key_len * 8;

	for (uint32_t bit = 0; bit < input_bits; ++bit) {
		if (input[bit / 8] & (0x80 >> (bit % 8))) {
			hash ^= window;
		}

		window <<= 1;

		uint32_t key_bit = bit + 32;
		if (key_bit < key_bits &&
		    (key[key_bit / 8] & (0x80 >> (key_bit % 8)))) {
			window |= 1;
		}
	}

	return hash;
}

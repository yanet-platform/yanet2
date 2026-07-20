#include "common/test_assert.h"
#include "common/thash.h"

#include "lib/logging/log.h"

#include <stdint.h>
#include <string.h>

// The 40-byte default RSS key and the IPv4-tuple RSS verification vectors
// from the Intel 82599 datasheet section 7.1.2.8.3, also used as the
// reference vectors in DPDK's own test_thash.c.
static const uint8_t default_rss_key[] = {
	0x6d, 0x5a, 0x56, 0xda, 0x25, 0x5b, 0x0e, 0xc2, 0x41, 0x67,
	0x25, 0x3d, 0x43, 0xa3, 0x8f, 0xb0, 0xd0, 0xca, 0x2b, 0xcb,
	0xae, 0x7b, 0x30, 0xb4, 0x77, 0xcb, 0x2d, 0xa3, 0x80, 0x30,
	0xf2, 0x0c, 0x6a, 0x42, 0xb7, 0x3b, 0xbe, 0xac, 0x01, 0xfa,
};

struct thash_v4_vector {
	uint8_t dst_ip[4];
	uint8_t src_ip[4];
	uint16_t dst_port;
	uint16_t src_port;
	uint32_t hash_l3;
	uint32_t hash_l3l4;
};

static const struct thash_v4_vector v4_vectors[] = {
	{{161, 142, 100, 80},
	 {66, 9, 149, 187},
	 1766,
	 2794,
	 0x323e8fc2,
	 0x51ccc178},
	{{65, 69, 140, 83},
	 {199, 92, 111, 2},
	 4739,
	 14230,
	 0xd718262a,
	 0xc626b0ea},
	{{12, 22, 207, 184},
	 {24, 19, 198, 95},
	 38024,
	 12898,
	 0xd2d0a5de,
	 0x5c2b394a},
	{{209, 142, 163, 6},
	 {38, 27, 205, 30},
	 2217,
	 48228,
	 0x82989176,
	 0xafc7327f},
	{{202, 188, 127, 2},
	 {153, 39, 163, 191},
	 1303,
	 44251,
	 0x5d1809c5,
	 0x10e828a2},
};

static void
put_u16_be(uint8_t *dst, uint16_t value) {
	dst[0] = (uint8_t)(value >> 8);
	dst[1] = (uint8_t)value;
}

// Verifies that thash_toeplitz reproduces the well-known RSS verification
// suite vectors, for both the L3-only tuple (source and destination
// address) and the L3+L4 tuple (address pair plus source and destination
// port). This is the empirical anchor that the algorithm matches the
// hardware Toeplitz hash bit-for-bit.
static int
test_v4_reference_vectors(void) {
	LOG(INFO, "Test IPv4 reference vectors...");

	for (size_t idx = 0; idx < sizeof(v4_vectors) / sizeof(v4_vectors[0]);
	     ++idx) {
		const struct thash_v4_vector *vector = v4_vectors + idx;

		uint8_t input_l3[8];
		memcpy(input_l3, vector->src_ip, 4);
		memcpy(input_l3 + 4, vector->dst_ip, 4);

		uint32_t hash_l3 = thash_toeplitz(
			default_rss_key,
			sizeof(default_rss_key),
			input_l3,
			sizeof(input_l3)
		);
		TEST_ASSERT_EQUAL(
			hash_l3, vector->hash_l3, "L3 hash mismatch at %zu", idx
		);

		uint8_t input_l3l4[12];
		memcpy(input_l3l4, input_l3, sizeof(input_l3));
		put_u16_be(input_l3l4 + 8, vector->src_port);
		put_u16_be(input_l3l4 + 10, vector->dst_port);

		uint32_t hash_l3l4 = thash_toeplitz(
			default_rss_key,
			sizeof(default_rss_key),
			input_l3l4,
			sizeof(input_l3l4)
		);
		TEST_ASSERT_EQUAL(
			hash_l3l4,
			vector->hash_l3l4,
			"L3+L4 hash mismatch at %zu",
			idx
		);
	}

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	if (test_v4_reference_vectors() != TEST_SUCCESS) {
		return -1;
	}

	LOG(INFO, "All thash tests passed");
	return 0;
}

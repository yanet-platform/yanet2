#include "common/crc32.h"
#include "common/test_assert.h"

#include "lib/logging/log.h"

#include <stdint.h>

struct crc32_vector {
	const char *name;
	const uint8_t *data;
	uint64_t size;
	uint32_t seed;
	uint32_t expected;
};

// These values were captured by running crc32() on the x86 build, which
// defines the wire-visible behaviour: packet->hash is computed from them
// and steers ECMP, MPLS entropy, pipeline and chain demux, and TX queue
// selection, so any architecture must reproduce these constants exactly.

static const uint8_t vec_len1[] = {0xab};
static const uint8_t vec_len2[] = {0x01, 0x02};
static const uint8_t vec_len3[] = {0x01, 0x02, 0x03};
static const uint8_t vec_len4[] = {0x01, 0x02, 0x03, 0x04};
static const uint8_t vec_len5[] = {0x01, 0x02, 0x03, 0x04, 0x05};
static const uint8_t vec_len6[] = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06};
static const uint8_t vec_len7[] = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07};
static const uint8_t vec_len8[] = {
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08
};
static const uint8_t vec_len13[] = {
	0x11,
	0x22,
	0x33,
	0x44,
	0x55,
	0x66,
	0x77,
	0x88,
	0x99,
	0xaa,
	0xbb,
	0xcc,
	0xdd,
};
static const uint8_t vec_len17[] = {
	0x01,
	0x02,
	0x03,
	0x04,
	0x05,
	0x06,
	0x07,
	0x08,
	0x09,
	0x0a,
	0x0b,
	0x0c,
	0x0d,
	0x0e,
	0x0f,
	0x10,
	0x11,
};

static const struct crc32_vector vectors[] = {
	{"empty, zero seed", NULL, 0, 0x00000000u, 0x00000000u},
	{"empty, non-zero seed", NULL, 0, 0xffffffffu, 0xffffffffu},
	{"1 byte", vec_len1, sizeof(vec_len1), 0x00000000u, 0x3bc21e9du},
	{"2 bytes", vec_len2, sizeof(vec_len2), 0x00000000u, 0xf299e880u},
	{"3 bytes", vec_len3, sizeof(vec_len3), 0x00000000u, 0x91545164u},
	{"4 bytes", vec_len4, sizeof(vec_len4), 0x00000000u, 0x6157c733u},
	{"5 bytes", vec_len5, sizeof(vec_len5), 0x00000000u, 0x1623f99eu},
	{"6 bytes", vec_len6, sizeof(vec_len6), 0x00000000u, 0x18678721u},
	{"7 bytes", vec_len7, sizeof(vec_len7), 0x00000000u, 0x06040eb1u},
	{"8 bytes", vec_len8, sizeof(vec_len8), 0x00000000u, 0xcaa1ad0bu},
	{"13 bytes, non-zero seed",
	 vec_len13,
	 sizeof(vec_len13),
	 0xdeadbeefu,
	 0xd103d910u},
	{"17 bytes", vec_len17, sizeof(vec_len17), 0x00000000u, 0xbdc13ac3u},
};

// Verifies that crc32() reproduces the golden values captured from the
// x86 build across an empty input, every tail length from 1 to 8 bytes
// (so the 8/4/2/1-byte branches each fire), an input past 8 bytes so the
// 8-byte loop iterates, an input past 16 bytes so the loop runs two full
// laps before the tail, and a non-zero seed.
static int
test_golden_vectors(void) {
	LOG(INFO, "Test golden vectors...");

	for (size_t idx = 0; idx < sizeof(vectors) / sizeof(vectors[0]);
	     ++idx) {
		const struct crc32_vector *vector = vectors + idx;
		uint32_t hash = crc32(vector->data, vector->size, vector->seed);
		TEST_ASSERT_EQUAL(
			hash,
			vector->expected,
			"crc32 mismatch for %s",
			vector->name
		);
	}

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	if (test_golden_vectors() != TEST_SUCCESS) {
		return -1;
	}

	LOG(INFO, "All crc32 tests passed");
	return 0;
}

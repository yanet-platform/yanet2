// Writes the C-built LPM images the Miri tests replay.
//
// Miri cannot run C, so the build script runs this program natively and
// the tests read its output from the build directory: for each key size,
// an LPM built with the C insert over fixed prefixes, captured block by
// block (header, chunk directory, chunks) at arena-relative logical
// addresses, plus the C lookup result for boundary and random keys. The
// encoding is the one the Rust fixture decoder reads: little-endian words,
// run-length encoded blocks. Invoked as fixture_gen <key-size> <output>.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/lpm.h"
#include "test_shim.h"

#define FIXTURE_MAGIC 0x59414e45544c504dULL
#define LOGICAL_BASE 0x10000000ULL
#define MAX_KEYS 64

struct prefix {
	uint8_t addr[16];
	unsigned len;
	uint32_t value;
};

static FILE *out;

static void
put(uint64_t word) {
	uint8_t bytes[8];
	for (int idx = 0; idx < 8; ++idx) {
		bytes[idx] = (uint8_t)(word >> (8 * idx));
	}
	fwrite(bytes, 1, sizeof(bytes), out);
}

static void
put_block(uint8_t *base, const uint8_t *block, size_t len) {
	put(LOGICAL_BASE + (uint64_t)(block - base));
	put(len);
	size_t words = len / 8;
	size_t runs = 0;
	for (size_t idx = 0; idx < words; ++idx) {
		if (idx == 0 ||
		    memcmp(block + idx * 8, block + (idx - 1) * 8, 8)) {
			++runs;
		}
	}
	put(runs);
	size_t idx = 0;
	while (idx < words) {
		size_t end = idx + 1;
		while (end < words &&
		       !memcmp(block + end * 8, block + idx * 8, 8)) {
			++end;
		}
		uint64_t value;
		memcpy(&value, block + idx * 8, 8);
		put(end - idx);
		put(value);
		idx = end;
	}
}

static void
prefix_range(
	const struct prefix *p, size_t key_size, uint8_t *from, uint8_t *to
) {
	memcpy(from, p->addr, key_size);
	memcpy(to, p->addr, key_size);
	for (size_t bit = p->len; bit < key_size * 8; ++bit) {
		uint8_t mask = (uint8_t)(0x80 >> (bit % 8));
		from[bit / 8] &= (uint8_t)~mask;
		to[bit / 8] |= mask;
	}
}

static uint64_t rng;

// xorshift64* as the Rust tests implement it.
static uint64_t
rng_next(void) {
	rng ^= rng >> 12;
	rng ^= rng << 25;
	rng ^= rng >> 27;
	return rng * 0x2545f4914f6cdd1dULL;
}

static void
rng_bytes(uint8_t *key) {
	for (int half = 0; half < 2; ++half) {
		uint64_t word = rng_next();
		for (int idx = 0; idx < 8; ++idx) {
			key[half * 8 + idx] = (uint8_t)(word >> (56 - 8 * idx));
		}
	}
}

// Adds a signed step to a big-endian key, carrying across bytes.
static void
key_add(uint8_t *key, size_t key_size, int delta) {
	for (size_t idx = key_size; idx-- > 0;) {
		int value = key[idx] + delta;
		key[idx] = (uint8_t)value;
		if (value >= 0 && value <= 255) {
			break;
		}
	}
}

int
main(int argc, char **argv) {
	if (argc != 3) {
		fprintf(stderr, "usage: %s <key-size> <output-file>\n", argv[0]
		);
		return 2;
	}
	size_t key_size = (size_t)atoi(argv[1]);
	struct prefix prefixes[6];
	size_t count = 0;
	memset(prefixes, 0, sizeof(prefixes));
	if (key_size == 4) {
		const struct prefix v4[] = {
			{{10, 0, 0, 0}, 8, 1},
			{{10, 1, 0, 0}, 16, 2},
			{{10, 1, 2, 0}, 24, 3},
			{{10, 1, 2, 3}, 32, 4},
			{{192, 168, 0, 0}, 16, 5},
			{{172, 16, 0, 0}, 12, 6},
		};
		memcpy(prefixes, v4, sizeof(v4));
		count = 6;
	} else if (key_size == 16) {
		const struct prefix v6[] = {
			{{0x20, 0x01, 0x0d, 0xb8}, 32, 11},
			{{0x20, 0x01, 0x0d, 0xb8, 0x12, 0x34}, 48, 12},
			{{0x20, 0x01, 0x0d, 0xb8, 0x12, 0x34, [15] = 0x01},
			 128,
			 13},
			{{0xfd}, 8, 14},
		};
		memcpy(prefixes, v6, sizeof(v6));
		count = 4;
	} else {
		fprintf(stderr, "key size must be 4 or 16\n");
		return 2;
	}

	struct yanet_sys_test_arena *arena = yanet_sys_test_arena_new(4 << 20);
	struct lpm *lpm = arena ? yanet_sys_test_lpm_new(arena) : NULL;
	if (lpm == NULL) {
		fprintf(stderr, "test arena allocation failed\n");
		return 1;
	}
	uint8_t keys[MAX_KEYS][16];
	size_t key_count = 0;
	memset(keys, 0, sizeof(keys));
	for (size_t idx = 0; idx < count; ++idx) {
		uint8_t from[16], to[16];
		prefix_range(&prefixes[idx], key_size, from, to);
		if (yanet_sys_test_lpm_insert(
			    lpm,
			    (uint8_t)key_size,
			    from,
			    to,
			    prefixes[idx].value
		    )) {
			fprintf(stderr, "LPM insert failed\n");
			return 1;
		}
		const uint8_t *edges[2] = {from, to};
		for (int edge = 0; edge < 2; ++edge) {
			memcpy(keys[key_count++], edges[edge], key_size);
			for (int delta = -1; delta <= 1; delta += 2) {
				memcpy(keys[key_count], edges[edge], key_size);
				key_add(keys[key_count++], key_size, delta);
			}
		}
	}
	// Boundary keys: three per prefix edge; the random ones follow.
	_Static_assert(6 * 2 * 3 + 16 <= MAX_KEYS, "fixture keys overflow");
	rng = 0x5eed0000ULL + key_size;
	for (int idx = 0; idx < 16; ++idx) {
		rng_bytes(keys[key_count++]);
	}

	out = fopen(argv[2], "wb");
	if (out == NULL) {
		perror(argv[2]);
		return 1;
	}
	uint8_t *base = yanet_sys_test_arena_base(arena);
	struct lpm_page **pages = ADDR_OF(&lpm->pages);
	size_t chunks = (lpm->page_count + LPM_CHUNK_SIZE - 1) / LPM_CHUNK_SIZE;
	put(FIXTURE_MAGIC);
	put(key_size);
	put(LOGICAL_BASE + (uint64_t)((uint8_t *)lpm - base));
	put(2 + chunks);
	put_block(base, (const uint8_t *)lpm, sizeof(*lpm));
	put_block(base, (const uint8_t *)pages, chunks * sizeof(*pages));
	for (size_t idx = 0; idx < chunks; ++idx) {
		put_block(
			base,
			(const uint8_t *)ADDR_OF(&pages[idx]),
			sizeof(struct lpm_page) * LPM_CHUNK_SIZE
		);
	}
	put(key_count);
	for (size_t idx = 0; idx < key_count; ++idx) {
		uint64_t low = 0, high = 0;
		for (int byte = 0; byte < 8; ++byte) {
			low |= (uint64_t)keys[idx][byte] << (8 * byte);
			high |= (uint64_t)keys[idx][8 + byte] << (8 * byte);
		}
		put(low);
		put(high);
		put(yanet_sys_test_lpm_lookup(lpm, (uint8_t)key_size, keys[idx])
		);
	}
	return fclose(out) == 0 ? 0 : 1;
}

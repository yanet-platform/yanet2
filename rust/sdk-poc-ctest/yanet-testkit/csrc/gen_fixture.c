// Writes the C-built decap image the Miri tests load.
//
// Miri cannot call C, so the build script runs this program natively and the
// tests embed its output: the image bytes, the configuration offset, and
// keys with the results the C lookup returned for them. Generation is
// deterministic.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "lpm_image.h"

#define IMAGE_SIZE (4u << 20)
#define PREFIXES4 192
#define PREFIXES6 48
#define KEYS4 512
#define KEYS6 256

static uint64_t rng_state = 0x9e3779b97f4a7c15ull;

static uint64_t
rng(void) {
	rng_state ^= rng_state << 13;
	rng_state ^= rng_state >> 7;
	rng_state ^= rng_state << 17;
	return rng_state;
}

// Fills [from, to] for a random prefix of length in [min_len, max_len].
static void
random_prefix(
	uint8_t *from,
	uint8_t *to,
	size_t size,
	unsigned min_len,
	unsigned max_len
) {
	unsigned len = min_len + (unsigned)(rng() % (max_len - min_len + 1));
	for (size_t i = 0; i < size; ++i) {
		from[i] = (uint8_t)rng();
	}
	for (size_t bit = len; bit < size * 8; ++bit) {
		from[bit / 8] &= (uint8_t)~(0x80u >> (bit % 8));
	}
	memcpy(to, from, size);
	for (size_t bit = len; bit < size * 8; ++bit) {
		to[bit / 8] |= (uint8_t)(0x80u >> (bit % 8));
	}
}

static int
write_file(const char *dir, const char *name, const void *data, size_t size) {
	char path[4096];
	snprintf(path, sizeof(path), "%s/%s", dir, name);
	FILE *file = fopen(path, "wb");
	if (file == NULL) {
		return -1;
	}
	size_t written = fwrite(data, 1, size, file);
	return fclose(file) || written != size ? -1 : 0;
}

// Records keys near inserted prefixes and random ones with their C results.
static int
write_keys(
	const struct tk_image *image,
	int v6,
	const uint8_t *seeds,
	size_t seed_count,
	size_t count,
	const char *dir,
	const char *name
) {
	size_t size = v6 ? 16 : 4;
	size_t record = size + sizeof(uint32_t);
	uint8_t *out = malloc(count * record);
	if (out == NULL) {
		return -1;
	}
	for (size_t i = 0; i < count; ++i) {
		uint8_t *key = out + i * record;
		if (i % 2 == 0) {
			memcpy(key, seeds + (rng() % seed_count) * size, size);
			key[size - 1 - (rng() % size)] ^= (uint8_t)rng();
		} else {
			for (size_t b = 0; b < size; ++b) {
				key[b] = (uint8_t)rng();
			}
		}
		uint32_t result = tk_image_lookup(image, v6, key);
		memcpy(key + size, &result, sizeof(result));
	}
	int rc = write_file(dir, name, out, count * record);
	free(out);
	return rc;
}

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: %s OUT_DIR\n", argv[0]);
		return 2;
	}
	struct tk_image *image = tk_image_new(IMAGE_SIZE);
	if (image == NULL) {
		return 1;
	}

	uint8_t seeds4[PREFIXES4 * 4], seeds6[PREFIXES6 * 16];
	for (size_t i = 0; i < PREFIXES4; ++i) {
		uint8_t to[4];
		random_prefix(seeds4 + i * 4, to, 4, 8, 32);
		if (tk_image_insert(
			    image, 0, seeds4 + i * 4, to, (uint32_t)(i + 1)
		    )) {
			return 1;
		}
	}
	for (size_t i = 0; i < PREFIXES6; ++i) {
		uint8_t to[16];
		random_prefix(seeds6 + i * 16, to, 16, 16, 64);
		if (tk_image_insert(
			    image, 1, seeds6 + i * 16, to, (uint32_t)(i + 1000)
		    )) {
			return 1;
		}
	}

	if (write_file(argv[1], "image.bin", image->base, image->size) ||
	    write_keys(
		    image, 0, seeds4, PREFIXES4, KEYS4, argv[1], "keys4.bin"
	    ) ||
	    write_keys(
		    image, 1, seeds6, PREFIXES6, KEYS6, argv[1], "keys6.bin"
	    )) {
		return 1;
	}
	printf("%zu\n", image->config_offset);
	tk_image_free(image);
	return 0;
}

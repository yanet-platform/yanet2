#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "lib/logging/log.h"

extern int
LLVMFuzzerTestOneInput(const unsigned char *data, size_t size); // NOLINT

/**
 * Split input data into random-sized chunks and call fuzzer for each chunk
 *
 * @param data Input data buffer
 * @param size Total size of input data
 * @return Number of chunks processed
 */
static int
split_and_fuzz(const uint8_t *data, size_t size) {
	if (size == 0) {
		return 0;
	}

	// Seed random number generator
	srand(time(NULL));

	size_t offset = 0;
	int chunk_count = 0;

	LOG(INFO,
	    "Splitting input file into random chunks (total size: %zu bytes)",
	    size);

	while (offset < size) {
		// Generate random chunk size between 64 and 1500 bytes (typical
		// packet sizes) But don't exceed remaining data
		size_t min_chunk = 64;
		size_t max_chunk = 1500;
		size_t remaining = size - offset;

		if (remaining < min_chunk) {
			// Process remaining data as final chunk
			max_chunk = remaining;
			min_chunk = remaining;
		} else if (remaining < max_chunk) {
			max_chunk = remaining;
		}

		size_t chunk_size =
			min_chunk + (rand() % (max_chunk - min_chunk + 1));

		LOG(DEBUG,
		    "Processing chunk %d: offset=%zu, size=%zu",
		    chunk_count + 1,
		    offset,
		    chunk_size);

		// Call fuzzer with this chunk
		LLVMFuzzerTestOneInput(data + offset, chunk_size);

		offset += chunk_size;
		chunk_count++;
	}

	LOG(INFO, "Processed %d chunks from input file", chunk_count);
	return chunk_count;
}

int
main(int argc, char **argv) {
	// Configure log level from environment variable, default to INFO
	const char *log_level = getenv("FUZZING_LOG_LEVEL");
	if (log_level == NULL || log_level[0] == '\0') {
		log_level = "INFO";
	}
	log_enable_name(log_level);

	if (argc < 2) {
		fprintf(stderr, "Usage: %s input-file\n", argv[0]);
		return 1;
	}

	LOG(INFO, "Opening file: %s", argv[1]);
	FILE *f = fopen(argv[1], "r");
	if (!f) {
		LOG(ERROR, "Failed to open file: %s", argv[1]);
		return 1;
	}

	if (fseek(f, 0, SEEK_END) != 0) {
		LOG(ERROR, "Failed to seek to end of file");
		fclose(f);
		return 1;
	}

	long size = ftell(f);
	if (size < 0) {
		LOG(ERROR, "Failed to get file size");
		fclose(f);
		return 1;
	}

	if (fseek(f, 0, SEEK_SET) != 0) {
		LOG(ERROR, "Failed to seek to start of file");
		fclose(f);
		return 1;
	}

	uint8_t *buf = malloc(size);
	if (!buf) {
		LOG(ERROR, "Failed to allocate %ld bytes", size);
		fclose(f);
		return 1;
	}

	LOG(INFO, "Reading %ld bytes from %s", size, argv[1]);
	size_t n_read = fread(buf, 1, size, f);
	fclose(f);

	if (n_read != (size_t)size) {
		LOG(ERROR,
		    "Failed to read file: expected %ld bytes, got %zu",
		    size,
		    n_read);
		free(buf);
		return 1;
	}

	// Check if we should split input into multiple packets
	const char *split_mode = getenv("FUZZING_SPLIT_INPUT");

	if (split_mode != NULL &&
	    (strcmp(split_mode, "1") == 0 || strcmp(split_mode, "true") == 0 ||
	     strcmp(split_mode, "yes") == 0)) {
		LOG(INFO,
		    "Split mode enabled - processing input as multiple packets"
		);
		split_and_fuzz(buf, (size_t)size);
	} else {
		LOG(INFO, "Testing input as single packet");
		LLVMFuzzerTestOneInput(buf, (size_t)size);
	}

	free(buf);

	LOG(INFO, "Test completed successfully");
	return 0;
}
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

extern int
LLVMFuzzerTestOneInput(const unsigned char *data, size_t size); // NOLINT

int
main(int argc, char **argv) {
	if (argc < 2) {
		fprintf(stderr, "Usage: %s input-file", argv[0]);
		return 1;
	}

	printf("Opening %s!\n", argv[1]);
	FILE *f = fopen(argv[1], "r");
	assert(f);
	fseek(f, 0, SEEK_END);
	ssize_t size = ftell(f);
	assert(size >= 0);
	fseek(f, 0, SEEK_SET);

	uint8_t *buf = malloc(size);
	printf("Reading %s!\n", argv[1]);
	size_t n_read = fread(buf, 1, size, f);
	fclose(f);
	assert(n_read == (size_t)size);

	printf("Testing %s!\n", argv[1]);
	LLVMFuzzerTestOneInput(buf, (size_t)size);
	free(buf);

	printf("Done!\n");
	return 0;
}

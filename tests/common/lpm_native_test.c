/*
 * Checks the prefix trees the native control plane builds from Go against
 * the trees common/lpm.h builds from the same inserts.
 *
 * The Go generator, the only argument, writes its cases into a file in
 * /dev/shm: for each one a region holding the tree as built and a region
 * holding it after the Go free. For every case the test builds the tree in
 * a region of its own laid out the same way and compares the two byte for
 * byte, looks the boundary keys of every insert up in the Go tree, compares
 * the regions after C's free and Go's, and frees the Go tree to see its
 * allocator get every byte back. The file is removed on every exit path.
 */

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <fcntl.h>
#include <inttypes.h>
#include <spawn.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

// The layout of the generator's file, which its documentation describes.
#define LPM_NATIVE_MAGIC 0x6c706d7472656573ULL
#define LPM_NATIVE_KEY_SIZE 16
#define LPM_NATIVE_MAX_INSERTS 64
#define LPM_NATIVE_NAME_SIZE 64

// The exit status meson reports as a skipped test.
#define TEST_SKIPPED 77

extern char **environ;

// An insert, its keys in their first key size bytes.
struct lpm_native_insert {
	uint8_t from[LPM_NATIVE_KEY_SIZE];
	uint8_t to[LPM_NATIVE_KEY_SIZE];
	uint32_t value;
	uint32_t reserved;
};

// A case: its tree's key size, its inserts and its two regions.
struct lpm_native_case {
	char name[LPM_NATIVE_NAME_SIZE];
	uint64_t key_size;
	// File offsets of the region holding the tree as built and of the one
	// holding it after the Go free.
	uint64_t built;
	uint64_t freed;
	uint64_t insert_count;
	struct lpm_native_insert inserts[LPM_NATIVE_MAX_INSERTS];
};

// The file's header, followed by its cases.
struct lpm_native_header {
	uint64_t magic;
	uint64_t red_zone;
	uint64_t filler;
	uint64_t region_size;
	// Offsets within a region; the arena runs to the region's end.
	uint64_t allocator;
	uint64_t context;
	uint64_t tree;
	uint64_t arena;
	uint64_t case_size;
	uint64_t case_count;
	struct lpm_native_case cases[];
};

// A region's allocator, root memory context and tree.
struct region {
	struct block_allocator *allocator;
	struct memory_context *context;
	struct lpm *tree;
};

// Returns the allocator, root memory context and tree of the region.
static struct region
region_at(const struct lpm_native_header *header, uint8_t *base) {
	return (struct region){
		.allocator =
			(struct block_allocator *)(base + header->allocator),
		.context = (struct memory_context *)(base + header->context),
		.tree = (struct lpm *)(base + header->tree),
	};
}

/*
 * Fills the region with the filler and lays out its allocator over its
 * arena and its root memory context, in the generator's order.
 */
static struct region
region_layout(const struct lpm_native_header *header, uint8_t *base) {
	memset(base, (int)header->filler, header->region_size);
	struct region region = region_at(header, base);
	block_allocator_init(region.allocator);
	block_allocator_put_arena(
		region.allocator,
		base + header->arena,
		header->region_size - header->arena
	);
	memory_context_init(region.context, "root", region.allocator);
	return region;
}

// Lays out the region and builds the case's tree in it from its inserts.
static int
region_build(
	const struct lpm_native_header *header,
	const struct lpm_native_case *tree_case,
	uint8_t *base
) {
	struct region region = region_layout(header, base);
	if (lpm_init(region.tree, region.context, "tree")) {
		fprintf(stdout, "%s: lpm_init failed\n", tree_case->name);
		return -1;
	}
	for (uint64_t idx = 0; idx < tree_case->insert_count; ++idx) {
		const struct lpm_native_insert *insert =
			tree_case->inserts + idx;
		if (lpm_insert(
			    region.tree,
			    (uint8_t)tree_case->key_size,
			    insert->from,
			    insert->to,
			    insert->value
		    )) {
			fprintf(stdout,
				"%s: lpm_insert %" PRIu64 " failed\n",
				tree_case->name,
				idx);
			return -1;
		}
	}
	return 0;
}

// Reports the first byte where the generator's region differs from C's.
static int
region_compare(
	const struct lpm_native_case *tree_case,
	const char *stage,
	const uint8_t *want,
	const uint8_t *got,
	size_t size
) {
	if (memcmp(want, got, size) == 0) {
		return 0;
	}
	size_t offset = 0;
	while (want[offset] == got[offset]) {
		++offset;
	}
	fprintf(stdout,
		"%s: the region %s differs first at offset %#zx: %#x, want "
		"%#x\n",
		tree_case->name,
		stage,
		offset,
		got[offset],
		want[offset]);
	return -1;
}

/*
 * verifies that the tree Go built leaves its region as C building it from
 * the same inserts leaves one.
 */
static int
run_build_matches_c_test(
	const struct lpm_native_header *header,
	const struct lpm_native_case *tree_case,
	const uint8_t *file,
	uint8_t *scratch
) {
	if (region_build(header, tree_case, scratch)) {
		return -1;
	}
	return region_compare(
		tree_case,
		"as built",
		scratch,
		file + tree_case->built,
		header->region_size
	);
}

/*
 * verifies that the tree Go built and then freed leaves its region as C
 * building and freeing it leaves one.
 */
static int
run_free_matches_c_test(
	const struct lpm_native_header *header,
	const struct lpm_native_case *tree_case,
	const uint8_t *file,
	uint8_t *scratch
) {
	if (region_build(header, tree_case, scratch)) {
		return -1;
	}
	lpm_free(region_at(header, scratch).tree);
	return region_compare(
		tree_case,
		"after the free",
		scratch,
		file + tree_case->freed,
		header->region_size
	);
}

// Returns the value of the last insert covering the key, as the tree maps.
static uint32_t
expected_value(const struct lpm_native_case *tree_case, const uint8_t *key) {
	size_t key_size = tree_case->key_size;
	for (uint64_t idx = tree_case->insert_count; idx-- > 0;) {
		const struct lpm_native_insert *insert =
			tree_case->inserts + idx;
		if (memcmp(insert->from, key, key_size) <= 0 &&
		    memcmp(key, insert->to, key_size) <= 0) {
			return insert->value;
		}
	}
	return LPM_VALUE_INVALID;
}

// Steps a big-endian key one up or down; fails when the key wraps.
static bool
key_step(uint8_t *key, size_t key_size, bool up) {
	for (size_t idx = key_size; idx-- > 0;) {
		uint8_t old = key[idx];
		key[idx] = up ? old + 1 : old - 1;
		if (old != (up ? 0xff : 0x00)) {
			return true;
		}
	}
	return false;
}

// Looks the key up in the tree and compares the value with the inserts'.
static int
lookup_compare(
	const struct lpm_native_case *tree_case,
	const struct lpm *tree,
	const uint8_t *key
) {
	uint8_t key_size = (uint8_t)tree_case->key_size;
	uint32_t got = lpm_lookup(tree, key_size, key);
	uint32_t want = expected_value(tree_case, key);
	if (got == want) {
		return 0;
	}
	fprintf(stdout, "%s: key", tree_case->name);
	for (uint8_t idx = 0; idx < key_size; ++idx) {
		fprintf(stdout, " %02x", key[idx]);
	}
	fprintf(stdout, " looks up %#x, want %#x\n", got, want);
	return -1;
}

/*
 * verifies that C's lookup finds, in the tree Go built, the value of the
 * last insert covering each end of every insert and the key past each end.
 */
static int
run_lookup_test(
	const struct lpm_native_header *header,
	const struct lpm_native_case *tree_case,
	uint8_t *file
) {
	const struct lpm *tree =
		region_at(header, file + tree_case->built).tree;
	size_t key_size = tree_case->key_size;
	for (uint64_t idx = 0; idx < tree_case->insert_count; ++idx) {
		const struct lpm_native_insert *insert =
			tree_case->inserts + idx;
		uint8_t key[LPM_NATIVE_KEY_SIZE];

		memcpy(key, insert->from, key_size);
		if (lookup_compare(tree_case, tree, key)) {
			return -1;
		}
		if (key_step(key, key_size, false) &&
		    lookup_compare(tree_case, tree, key)) {
			return -1;
		}

		memcpy(key, insert->to, key_size);
		if (lookup_compare(tree_case, tree, key)) {
			return -1;
		}
		if (key_step(key, key_size, true) &&
		    lookup_compare(tree_case, tree, key)) {
			return -1;
		}
	}
	return 0;
}

/*
 * verifies that C's free of the tree Go built gives its allocator back
 * every byte of the arena and unlinks the tree's memory context.
 */
static int
run_free_returns_memory_test(
	const struct lpm_native_header *header,
	const struct lpm_native_case *tree_case,
	uint8_t *file,
	uint8_t *scratch
) {
	size_t arena = block_allocator_free_size(
		region_layout(header, scratch).allocator
	);
	struct region region = region_at(header, file + tree_case->built);
	lpm_free(region.tree);

	size_t free = block_allocator_free_size(region.allocator);
	if (free != arena) {
		fprintf(stdout,
			"%s: the allocator holds %zu free bytes after the "
			"free, "
			"want %zu\n",
			tree_case->name,
			free,
			arena);
		return -1;
	}
	if (ADDR_OF(&region.context->first_child) != NULL) {
		fprintf(stdout,
			"%s: the tree's memory context is still linked\n",
			tree_case->name);
		return -1;
	}
	return 0;
}

// Runs the generator to write the file of that name and waits for it.
static int
run_generator(const char *generator, const char *name) {
	char *argv[] = {(char *)generator, (char *)name, NULL};
	pid_t pid;
	int rc = posix_spawn(&pid, generator, NULL, NULL, argv, environ);
	if (rc != 0) {
		fprintf(stdout, "spawn %s: %s\n", generator, strerror(rc));
		return -1;
	}
	int status;
	if (waitpid(pid, &status, 0) != pid) {
		perror("waitpid");
		return -1;
	}
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
		fprintf(stdout, "%s failed with status %#x\n", generator, status
		);
		return -1;
	}
	return 0;
}

/*
 * Checks that the header describes cases and regions that lie inside the
 * file and that C lays out the same way.
 *
 * Returns 0 when they do, TEST_SKIPPED when the generator's allocator puts
 * another red zone around its blocks than this build's does, and -1 on a
 * header this test cannot read.
 */
static int
header_check(const struct lpm_native_header *header, size_t size) {
	if (size < sizeof(*header) || header->magic != LPM_NATIVE_MAGIC ||
	    header->case_size != sizeof(struct lpm_native_case) ||
	    header->case_count >
		    (size - sizeof(*header)) / sizeof(struct lpm_native_case)) {
		fprintf(stdout, "the file's header is not the generator's\n");
		return -1;
	}
	if (header->red_zone != ASAN_RED_ZONE) {
		fprintf(stdout,
			"skipped: the generator's blocks carry %" PRIu64
			"-byte red "
			"zones, this build's %d-byte ones\n",
			header->red_zone,
			ASAN_RED_ZONE);
		return TEST_SKIPPED;
	}
	uint64_t region_size = header->region_size;
	if (region_size == 0 || (region_size & (region_size - 1)) != 0 ||
	    region_size > MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN ||
	    header->allocator + sizeof(struct block_allocator) >
		    header->context ||
	    header->context + sizeof(struct memory_context) > header->tree ||
	    header->tree + sizeof(struct lpm) > header->arena ||
	    header->arena >= region_size) {
		fprintf(stdout, "the regions overlap or are misaligned\n");
		return -1;
	}
	for (uint64_t idx = 0; idx < header->case_count; ++idx) {
		const struct lpm_native_case *tree_case = header->cases + idx;
		if (tree_case->key_size == 0 ||
		    tree_case->key_size > LPM_NATIVE_KEY_SIZE ||
		    tree_case->insert_count > LPM_NATIVE_MAX_INSERTS ||
		    tree_case->built > size - region_size ||
		    tree_case->freed > size - region_size ||
		    tree_case->name[LPM_NATIVE_NAME_SIZE - 1] != '\0') {
			fprintf(stdout,
				"case %" PRIu64 " is out of bounds\n",
				idx);
			return -1;
		}
	}
	return 0;
}

// Runs every check of every case in the file, stopping at the first fault.
static int
run_cases(const struct lpm_native_header *header, uint8_t *file) {
	uint8_t *scratch = NULL;
	if (posix_memalign(
		    (void **)&scratch, header->region_size, header->region_size
	    )) {
		fprintf(stdout, "could not allocate the scratch region\n");
		return -1;
	}

	int rc = 0;
	for (uint64_t idx = 0; rc == 0 && idx < header->case_count; ++idx) {
		const struct lpm_native_case *tree_case = header->cases + idx;
		if (run_build_matches_c_test(
			    header, tree_case, file, scratch
		    ) ||
		    run_free_matches_c_test(header, tree_case, file, scratch) ||
		    run_lookup_test(header, tree_case, file) ||
		    run_free_returns_memory_test(
			    header, tree_case, file, scratch
		    )) {
			rc = -1;
		}
	}
	free(scratch);
	return rc;
}

// Maps the file the generator wrote and checks every case in it.
static int
check_file(const char *path) {
	int fd = open(path, O_RDONLY);
	if (fd < 0) {
		perror(path);
		return -1;
	}
	struct stat info;
	if (fstat(fd, &info)) {
		perror(path);
		close(fd);
		return -1;
	}
	size_t size = (size_t)info.st_size;
	// A private mapping takes the frees without changing the file.
	uint8_t *file =
		mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_PRIVATE, fd, 0);
	close(fd);
	if (file == MAP_FAILED) {
		perror(path);
		return -1;
	}

	const struct lpm_native_header *header =
		(const struct lpm_native_header *)file;
	int rc = header_check(header, size);
	if (rc == 0) {
		rc = run_cases(header, file);
	}
	munmap(file, size);
	return rc;
}

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stdout, "usage: %s GENERATOR\n", argv[0]);
		return 2;
	}

	char name[64];
	char path[80];
	snprintf(name, sizeof(name), "yanet-lpm-native-%d", (int)getpid());
	snprintf(path, sizeof(path), "/dev/shm/%s", name);

	int rc = run_generator(argv[1], name);
	if (rc == 0) {
		rc = check_file(path);
	}
	unlink(path);

	if (rc == TEST_SKIPPED) {
		return TEST_SKIPPED;
	}
	if (rc != 0) {
		fprintf(stdout, "lpm_native: FAILED\n");
		return 1;
	}
	return 0;
}

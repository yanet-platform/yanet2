/*
 * Checks the classifiers the native control plane compiles from Go
 * against the classifiers lib/classify compiles from the same rules.
 *
 * The Go generator, the only argument, writes its cases into a file in
 * /dev/shm: for each one its rules as flat records and a region holding
 * the attribute Go committed. For every case the test compiles the same
 * rules with lib/classify in memory of its own, requires both attributes
 * to give every key of every elementary interval of the domain one class
 * and their memory contexts one name, then frees the Go attribute with
 * C's free to see its allocator get every byte back. The file is removed
 * on every exit path.
 */

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"

#include <arpa/inet.h>
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
#define CLASSIFY_NATIVE_MAGIC 0x636c617373696679ULL
#define CLASSIFY_NATIVE_NAME_SIZE 128
#define CLASSIFY_NATIVE_LINE 1
#define CLASSIFY_NATIVE_NET4 2
// An item: a struct classify_line_range or a struct net4.
#define CLASSIFY_NATIVE_ITEM_SIZE 8

// The memory C compiles a case in: its scratch outgrows the attribute.
#define SCRATCH_SIZE (256 << 20)

// The exit status meson reports as a skipped test.
#define TEST_SKIPPED 77

extern char **environ;

// A rule: a run of its case's items, or none for an absent rule.
struct classify_native_rule {
	uint32_t first;
	uint32_t count;
	uint32_t absent;
	uint32_t reserved;
};

// A case: the attribute it compiles, its rules and its region.
struct classify_native_case {
	char name[CLASSIFY_NATIVE_NAME_SIZE];
	uint32_t kind;
	// The size of a line's domain.
	uint32_t domain;
	uint64_t rule_count;
	uint64_t item_count;
	// File offsets of the rules, the items and the region.
	uint64_t rules;
	uint64_t items;
	uint64_t region;
	// What the region's allocator held free before the commit.
	uint64_t free;
};

// The file's header, followed by its cases.
struct classify_native_header {
	uint64_t magic;
	uint64_t red_zone;
	uint64_t region_size;
	// Offsets within a region; the arena runs to the region's end.
	uint64_t allocator;
	uint64_t context;
	uint64_t line;
	uint64_t net4;
	uint64_t arena;
	uint64_t case_size;
	uint64_t case_count;
	struct classify_native_case cases[];
};

// A module rule as the getters read it: a view over the case's items.
struct native_rule {
	struct classifier_rule rule;
	void *items;
	uint32_t count;
};

static void
native_get_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct native_rule *native =
		container_of(rule, struct native_rule, rule);
	ranges->items = (const struct classify_line_range *)native->items;
	ranges->count = native->count;
}

static void
native_get_net4s(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct native_rule *native =
		container_of(rule, struct native_rule, rule);
	nets->items = (struct net4 *)native->items;
	nets->count = native->count;
}

CLASSIFY_LINE_COMPILE(
	native_line_byte, struct classify_attr_line, 0x100, native_get_ranges
)
CLASSIFY_LINE_COMPILE(
	native_line_port, struct classify_attr_line, 0x10000, native_get_ranges
)

// The memory a case is compiled in: an allocator, its root context and
// the attribute, then the arena.
struct scratch {
	struct block_allocator allocator;
	struct memory_context context;
	struct classify_attr_line line;
	struct classify_attr_net4 net4;
	struct classifier cls;
};

// The rules of a case, built from its records, with NULL for an absent
// one.
struct case_rules {
	struct native_rule *rules;
	const struct classifier_rule **slots;
};

// Builds the case's rules from its records, pointing into its items.
static int
case_rules_build(
	const struct classify_native_case *tc,
	uint8_t *file,
	struct case_rules *built
) {
	uint64_t count = tc->rule_count ? tc->rule_count : 1;
	built->rules = calloc(count, sizeof(*built->rules));
	built->slots = calloc(count, sizeof(*built->slots));
	if (built->rules == NULL || built->slots == NULL) {
		fprintf(stdout, "%s: could not allocate the rules\n", tc->name);
		return -1;
	}
	const struct classify_native_rule *records =
		(const struct classify_native_rule *)(file + tc->rules);
	for (uint64_t idx = 0; idx < tc->rule_count; ++idx) {
		const struct classify_native_rule *record = records + idx;
		built->rules[idx].items =
			file + tc->items +
			CLASSIFY_NATIVE_ITEM_SIZE * record->first;
		built->rules[idx].count = record->count;
		built->slots[idx] =
			record->absent ? NULL : &built->rules[idx].rule;
	}
	return 0;
}

static void
case_rules_free(struct case_rules *built) {
	free(built->rules);
	free(built->slots);
}

// Lays out the scratch's allocator over its arena and its root context.
static struct scratch *
scratch_layout(uint8_t *memory) {
	struct scratch *scratch = (struct scratch *)memory;
	memset(scratch, 0, sizeof(*scratch));
	block_allocator_init(&scratch->allocator);
	block_allocator_put_arena(
		&scratch->allocator,
		memory + sizeof(*scratch),
		SCRATCH_SIZE - sizeof(*scratch)
	);
	memory_context_init(&scratch->context, "root", &scratch->allocator);
	return scratch;
}

// A line compile entry, one per domain size.
typedef int (*line_compile_func)(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule **rules,
	uint32_t rule_count,
	struct classify_attr_line *attr,
	struct classifier *cls
);

// Compiles the case's line with the entry of its domain.
static int
compile_line(
	const struct classify_native_case *tc,
	struct case_rules *built,
	struct scratch *scratch
) {
	line_compile_func compile;
	switch (tc->domain) {
	case 0x100:
		compile = classify_native_line_byte_compile;
		break;
	case 0x10000:
		compile = classify_native_line_port_compile;
		break;
	default:
		fprintf(stdout,
			"%s: no line compile over %#x points\n",
			tc->name,
			tc->domain);
		return -1;
	}
	return compile(
		&scratch->context,
		tc->name,
		built->slots,
		(uint32_t)tc->rule_count,
		&scratch->line,
		&scratch->cls
	);
}

// Reports a memory context whose name differs from C's.
static int
name_compare(
	const struct classify_native_case *tc,
	const struct memory_context *want,
	const struct memory_context *got
) {
	if (strncmp(want->name, got->name, MEMORY_CONTEXT_NAME_SIZE) == 0) {
		return 0;
	}
	fprintf(stdout,
		"%s: the memory context is named \"%.*s\", want \"%.*s\"\n",
		tc->name,
		MEMORY_CONTEXT_NAME_SIZE,
		got->name,
		MEMORY_CONTEXT_NAME_SIZE,
		want->name);
	return -1;
}

/*
 * verifies that every point of the domain has the class in the line Go
 * committed that it has in C's, and the line's context C's name.
 */
static int
run_line_matches_c_test(
	const struct classify_native_case *tc,
	struct classify_attr_line *got,
	struct classify_attr_line *want
) {
	if (got->line.size != want->line.size) {
		fprintf(stdout,
			"%s: the line holds %u points, want %u\n",
			tc->name,
			got->line.size,
			want->line.size);
		return -1;
	}
	for (uint32_t point = 0; point < want->line.size; ++point) {
		uint32_t got_class = vline_get(&got->line, point);
		uint32_t want_class = vline_get(&want->line, point);
		if (got_class != want_class) {
			fprintf(stdout,
				"%s: point %#x is of class %u, want %u\n",
				tc->name,
				point,
				got_class,
				want_class);
			return -1;
		}
	}
	return name_compare(
		tc,
		ADDR_OF(&want->line.memory_context),
		ADDR_OF(&got->line.memory_context)
	);
}

// Looks the address up in both trees and compares the classes.
static int
net4_class_compare(
	const struct classify_native_case *tc,
	const struct classify_attr_net4 *got,
	const struct classify_attr_net4 *want,
	uint32_t addr
) {
	uint32_t key = htonl(addr);
	uint32_t got_class;
	uint32_t want_class;
	classify_net4_lookup(got, &key, &got_class, 1);
	classify_net4_lookup(want, &key, &want_class, 1);
	if (got_class == want_class) {
		return 0;
	}
	fprintf(stdout,
		"%s: address %#" PRIx32 " is of class %u, want %u\n",
		tc->name,
		addr,
		got_class,
		want_class);
	return -1;
}

/*
 * verifies that both ends of every elementary interval the networks cut
 * the address space into have the class in the tree Go committed that
 * they have in C's, and the tree's context C's name.
 */
static int
run_net4_matches_c_test(
	const struct classify_native_case *tc,
	const uint8_t *file,
	const struct classify_attr_net4 *got,
	const struct classify_attr_net4 *want
) {
	if (net4_class_compare(tc, got, want, 0) ||
	    net4_class_compare(tc, got, want, UINT32_MAX)) {
		return -1;
	}
	const struct net4 *nets = (const struct net4 *)(file + tc->items);
	for (uint64_t idx = 0; idx < tc->item_count; ++idx) {
		uint32_t addr;
		uint32_t mask;
		memcpy(&addr, nets[idx].addr, sizeof(addr));
		memcpy(&mask, nets[idx].mask, sizeof(mask));
		uint32_t from = ntohl(addr & mask);
		uint32_t to = ntohl(addr | ~mask);
		if (net4_class_compare(tc, got, want, from) ||
		    net4_class_compare(tc, got, want, from - 1) ||
		    net4_class_compare(tc, got, want, to) ||
		    net4_class_compare(tc, got, want, to + 1)) {
			return -1;
		}
	}
	return name_compare(
		tc, &want->lpm.memory_context, &got->lpm.memory_context
	);
}

/*
 * verifies that C's free of the attribute Go committed gives the region's
 * allocator back every byte the commit took and unlinks the attribute's
 * memory context.
 */
static int
run_free_returns_memory_test(
	const struct classify_native_header *header,
	const struct classify_native_case *tc,
	uint8_t *file
) {
	uint8_t *region = file + tc->region;
	struct block_allocator *allocator =
		(struct block_allocator *)(region + header->allocator);
	struct memory_context *context =
		(struct memory_context *)(region + header->context);
	if (tc->kind == CLASSIFY_NATIVE_LINE) {
		classify_attr_line_free(
			context,
			(struct classify_attr_line *)(region + header->line)
		);
	} else {
		classify_attr_net4_free(
			context,
			(struct classify_attr_net4 *)(region + header->net4)
		);
	}

	size_t free = block_allocator_free_size(allocator);
	if (free != tc->free) {
		fprintf(stdout,
			"%s: the allocator holds %zu free bytes after the "
			"free, want %" PRIu64 "\n",
			tc->name,
			free,
			tc->free);
		return -1;
	}
	if (ADDR_OF(&context->first_child) != NULL) {
		fprintf(stdout,
			"%s: the attribute's memory context is still linked\n",
			tc->name);
		return -1;
	}
	return 0;
}

// Compiles the case with lib/classify and runs its checks.
static int
run_case(
	const struct classify_native_header *header,
	const struct classify_native_case *tc,
	uint8_t *file,
	uint8_t *memory
) {
	struct case_rules built = {0};
	if (case_rules_build(tc, file, &built)) {
		case_rules_free(&built);
		return -1;
	}
	struct scratch *scratch = scratch_layout(memory);
	int rc = tc->kind == CLASSIFY_NATIVE_LINE
			 ? compile_line(tc, &built, scratch)
			 : classify_net4_compile(
				   &scratch->context,
				   tc->name,
				   built.slots,
				   (uint32_t)tc->rule_count,
				   native_get_net4s,
				   &scratch->net4,
				   &scratch->cls
			   );
	case_rules_free(&built);
	if (rc != 0) {
		fprintf(stdout, "%s: C's compile failed\n", tc->name);
		return -1;
	}

	uint8_t *region = file + tc->region;
	if (tc->kind == CLASSIFY_NATIVE_LINE) {
		rc = run_line_matches_c_test(
			tc,
			(struct classify_attr_line *)(region + header->line),
			&scratch->line
		);
	} else {
		rc = run_net4_matches_c_test(
			tc,
			file,
			(struct classify_attr_net4 *)(region + header->net4),
			&scratch->net4
		);
	}
	return rc ? rc : run_free_returns_memory_test(header, tc, file);
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
		fprintf(stdout, "%s failed: status %#x\n", generator, status);
		return -1;
	}
	return 0;
}

/*
 * Checks that the header describes cases, rules, items and regions that
 * lie inside the file and that C lays out the same way.
 *
 * Returns 0 when they do, TEST_SKIPPED when the generator's allocator puts
 * another red zone around its blocks than this build's does, and -1 on a
 * header this test cannot read.
 */
static int
header_check(const struct classify_native_header *header, size_t size) {
	if (size < sizeof(*header) || header->magic != CLASSIFY_NATIVE_MAGIC ||
	    header->case_size != sizeof(struct classify_native_case) ||
	    header->case_count > (size - sizeof(*header)) /
					 sizeof(struct classify_native_case)) {
		fprintf(stdout, "the file's header is not the generator's\n");
		return -1;
	}
	if (header->red_zone != ASAN_RED_ZONE) {
		fprintf(stdout,
			"skipped: the generator's blocks carry %" PRIu64
			"-byte red zones, this build's %d-byte ones\n",
			header->red_zone,
			ASAN_RED_ZONE);
		return TEST_SKIPPED;
	}
	uint64_t region_size = header->region_size;
	if (region_size == 0 || size < region_size ||
	    header->allocator + sizeof(struct block_allocator) >
		    header->context ||
	    header->context + sizeof(struct memory_context) > header->line ||
	    header->line + sizeof(struct classify_attr_line) > header->net4 ||
	    header->net4 + sizeof(struct classify_attr_net4) > header->arena ||
	    header->arena >= region_size) {
		fprintf(stdout, "the region's layout is not C's\n");
		return -1;
	}
	for (uint64_t idx = 0; idx < header->case_count; ++idx) {
		const struct classify_native_case *tc = header->cases + idx;
		if ((tc->kind != CLASSIFY_NATIVE_LINE &&
		     tc->kind != CLASSIFY_NATIVE_NET4) ||
		    tc->rule_count > UINT32_MAX || tc->rules > size ||
		    tc->rule_count > (size - tc->rules
				     ) / sizeof(struct classify_native_rule) ||
		    tc->items > size ||
		    tc->item_count >
			    (size - tc->items) / CLASSIFY_NATIVE_ITEM_SIZE ||
		    tc->region > size - region_size ||
		    tc->name[CLASSIFY_NATIVE_NAME_SIZE - 1] != '\0') {
			fprintf(stdout,
				"case %" PRIu64 " is out of bounds\n",
				idx);
			return -1;
		}
	}
	return 0;
}

// Runs every case in the file, stopping at the first fault.
static int
run_cases(const struct classify_native_header *header, uint8_t *file) {
	uint8_t *memory = NULL;
	if (posix_memalign((void **)&memory, 4096, SCRATCH_SIZE)) {
		fprintf(stdout, "could not allocate the scratch memory\n");
		return -1;
	}
	int rc = 0;
	for (uint64_t idx = 0; rc == 0 && idx < header->case_count; ++idx) {
		rc = run_case(header, header->cases + idx, file, memory);
	}
	free(memory);
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
	// The mapping outlives the name, so a crash in the checks leaves
	// no file behind.
	unlink(path);
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

	const struct classify_native_header *header =
		(const struct classify_native_header *)file;
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
	snprintf(name, sizeof(name), "yanet-classify-native-%d", (int)getpid());
	snprintf(path, sizeof(path), "/dev/shm/%s", name);

	// A file a killed run of a reused pid left would refuse the
	// generator's exclusive create.
	unlink(path);
	int rc = run_generator(argv[1], name);
	if (rc == 0) {
		rc = check_file(path);
	}
	unlink(path);

	if (rc == TEST_SKIPPED) {
		return TEST_SKIPPED;
	}
	if (rc != 0) {
		fprintf(stdout, "classify_native: FAILED\n");
		return 1;
	}
	return 0;
}

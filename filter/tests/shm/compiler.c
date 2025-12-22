#include "common.h"
#include "common/memory_block.h"
#include "filter.h"
#include "logging/log.h"
#include <sys/mman.h>

#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include "../utils/utils.h"

////////////////////////////////////////////////////////////////////////////////

int
build_filter(struct common *common, struct memory_context *mctx) {
	const size_t rule_count = 100;
	struct filter_rule_builder builders[rule_count];
	struct filter_rule rules[rule_count];
	uint64_t rng = 1231231;
	for (size_t i = 0; i < rule_count; ++i) {
		struct filter_rule_builder *builder = &builders[i];
		builder_init(builder);
		uint16_t from = rng_next(&rng) & 0xFF;
		uint16_t to = rng_next(&rng) & 0xFF;
		builder_add_port_dst_range(
			builder, RTE_MIN(from, to), RTE_MAX(from, to)
		);
		uint8_t addr[4] = {(i + 1) & 0xFF, 0, 0, 0};
		uint8_t mask[4];
		memset(mask, 0xFF, 4);
		builder_add_net4_dst(builder, addr, mask);
		builder_set_proto(
			builder, i % 2 == 0 ? IPPROTO_TCP : IPPROTO_UDP, 0, 0
		);
		rules[i] = build_rule(builder, i + 1);
	}
	LOG(INFO, "compiling %zu rules...", rule_count);
	int res = FILTER_INIT(
		&common->filter, filter_sign, rules, rule_count, mctx
	);
	if (res < 0) {
		LOG(ERROR, "compilation failed: %d", res);
		return 1;
	} else {
		LOG(INFO,
		    "compilation successful (used %.2lfMB)",
		    (double)common->filter.memory_context.balloc_size /
			    (1 << 20));
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

int
main(int argc, char **argv) {
	if (argc < 3) {
		printf("Usage: %s <SHM_PATH> <SHM_SIZE>\n", argv[0]);
		return 1;
	}

	// Enable logging
	// log_enable_name("error");
	// log_enable_name("info");
	log_enable_name("trace");

	LOG(INFO, "starting...");

	errno = 0;
	size_t size = atoi(argv[2]);
	if (errno != 0) {
		LOG(ERROR,
		    "atoi: failed to parse shared memory size (%s): %s",
		    argv[2],
		    strerror(errno));
		return 1;
	}

	const size_t require = 1 << 12;
	if (size < require) {
		LOG(ERROR,
		    "shared memory size if %lu, required size is at least "
		    "%lu\n",
		    size,
		    require);
		return 1;
	}

	LOG(INFO, "attaching to shared memory...");

	// Open shared memory file descriptor
	const char *shm_name_arg = argv[1];
	char shm_name_buf[NAME_MAX + 2];
	const char *shm_name;
	if (shm_name_arg[0] == '/') {
		shm_name = shm_name_arg;
	} else {
		int n = snprintf(
			shm_name_buf, sizeof(shm_name_buf), "/%s", shm_name_arg
		);
		if (n < 0 || (size_t)n >= sizeof(shm_name_buf)) {
			LOG(ERROR, "shared memory name too long");
			return 1;
		}
		shm_name = shm_name_buf;
	}
	int shm_fd = shm_open(shm_name, O_RDWR, 0);
	if (shm_fd == -1) {
		LOG(ERROR,
		    "shm_open('%s') failed: %s",
		    shm_name,
		    strerror(errno));
		return 1;
	}

	// MMap to the shared memory
	void *memory =
		mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, shm_fd, 0);
	if (memory == MAP_FAILED) {
		LOG(ERROR, "failed to mmap: %s", strerror(errno));
		close(shm_fd);
		return 1;
	}

	// Init common in the beginning of the shared memory
	struct common *common = (struct common *)memory;
	atomic_init(&common->ready, 0);
	memset(&common->filter, 0, sizeof(struct filter));

	struct block_allocator alloc;
	if (block_allocator_init(&alloc) < 0) {
		LOG(ERROR, "failed to init block allocator");
		return 1;
	}
	block_allocator_put_arena(&alloc, memory + require, size - require);

	// Init memory_context
	struct memory_context mctx;
	if (memory_context_init(&mctx, "compiler", &alloc) != 0) {
		LOG(ERROR, "failed to init memory context");
		return 1;
	}

	LOG(INFO, "successfully attached to shared memory (%p)", memory);

	LOG(INFO, "building filter...");

	// Build filter
	if (build_filter(common, &mctx) < 0) {
		LOG(ERROR, "failed to build filter: %s", strerror(errno));
		return 1;
	}

	// Success

	LOG(INFO, "successfully built filter");

	// Signal that filter is ready
	atomic_store(&common->ready, 1);

	// Unlink shared memory

	// if (shm_unlink(shm_name) < 0) {
	//     LOG(ERROR, "Failed to unlink shared memory: %s",
	//     strerror(errno)); return 1;
	// }

	return 0;
}
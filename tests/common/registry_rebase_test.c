// Regression test for struct value_registry / struct value_collector storing
// their memory_context as a raw virtual address instead of a shared-memory
// offset pointer.
//
// Builds an arena through one mapping of a memfd and finalises it through a
// second mapping of the same bytes, which is exactly what happens when the
// dataplane rebases a shared-memory config at a different base than the
// control plane that built it.

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/registry.h"
#include "common/test_assert.h"
#include "common/value.h"
#include "lib/logging/log.h"

#include <stdint.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

#define REGION_SIZE ((size_t)(16u << 20))

struct arena {
	struct block_allocator alloc;
	struct memory_context root;
	struct value_registry registry;
	struct value_table table;
};

// Builds allocator, root context, a populated registry and a table, all
// inside the mapping so every offset pointer in the chain is rebasable.
static void
build_arena(struct arena *a) {
	if (block_allocator_init(&a->alloc)) {
		_exit(4);
	}

	uintptr_t start = ((uintptr_t)a + sizeof(*a) + 63) & ~(uintptr_t)63;
	uintptr_t end = (uintptr_t)a + REGION_SIZE;
	block_allocator_put_arena(
		&a->alloc, (void *)start, (size_t)(end - start)
	);

	if (memory_context_init(&a->root, "root", &a->alloc)) {
		_exit(5);
	}
	if (value_registry_init(&a->registry, &a->root, "reg")) {
		_exit(6);
	}

	// Two generations with several values each, so both the collector's
	// use-map chunks and the ranges array really allocate and the fini
	// path has something to walk.
	for (int gen = 0; gen < 2; ++gen) {
		if (value_registry_start(&a->registry)) {
			_exit(7);
		}
		for (uint32_t v = 0; v < 24; ++v) {
			if (value_registry_collect(&a->registry, v + gen)) {
				_exit(8);
			}
		}
	}

	if (value_table_init(&a->table, &a->root, "tab", 8, 8)) {
		_exit(9);
	}
}

static int
make_memfd(void) {
	int fd = memfd_create("registry_rebase_test", 0);
	if (fd < 0) {
		return -1;
	}
	if (ftruncate(fd, REGION_SIZE) != 0) {
		close(fd);
		return -1;
	}
	return fd;
}

// Maps fd twice at kernel-chosen addresses backed by the same pages, so an
// arena built through one base is read through a genuinely different one.
static int
map_pair(int fd, struct arena **out_a, struct arena **out_b) {
	void *a = mmap(
		NULL, REGION_SIZE, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0
	);
	if (a == MAP_FAILED) {
		return -1;
	}
	void *b = mmap(
		NULL, REGION_SIZE, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0
	);
	if (b == MAP_FAILED) {
		munmap(a, REGION_SIZE);
		return -1;
	}
	if (a == b) {
		munmap(a, REGION_SIZE);
		munmap(b, REGION_SIZE);
		return -1;
	}
	*out_a = (struct arena *)a;
	*out_b = (struct arena *)b;
	return 0;
}

// Positive control: value_table_free, built through A and finalised through
// B with A unmapped.
//
// table.memory_context already uses the offset convention, so a genuine
// failure elsewhere is specific to the raw registry field, not the rebase
// mechanics.
static void
phase_table_free(void) {
	int fd = make_memfd();
	if (fd < 0) {
		_exit(3);
	}
	struct arena *a, *b;
	if (map_pair(fd, &a, &b)) {
		_exit(3);
	}
	build_arena(a);
	munmap(a, REGION_SIZE);

	value_table_free(&b->table);
	_exit(0);
}

// The fix: value_registry_fini, built through A and finalised through B
// with A unmapped.
static void
phase_registry_fini(void) {
	int fd = make_memfd();
	if (fd < 0) {
		_exit(3);
	}
	struct arena *a, *b;
	if (map_pair(fd, &a, &b)) {
		_exit(3);
	}
	build_arena(a);
	munmap(a, REGION_SIZE);

	value_registry_fini(&b->registry);
	_exit(0);
}

// Silent-corruption case: A stays mapped, so an unfixed raw pointer aliases
// live memory rather than faulting, and fini pushes blocks onto the free
// lists with a mismatched base.
//
// Checks that every pool's free-list head resolves inside b's mapping,
// instead of provoking the corruption with a further allocation.
static void
phase_registry_fini_wild(void) {
	int fd = make_memfd();
	if (fd < 0) {
		_exit(3);
	}
	struct arena *a, *b;
	if (map_pair(fd, &a, &b)) {
		_exit(3);
	}
	build_arena(a);

	value_registry_fini(&b->registry);

	for (size_t idx = 0; idx < MEMORY_BLOCK_ALLOCATOR_EXP; ++idx) {
		void *head = ADDR_OF(&b->alloc.pools[idx].free_list);
		if (head == NULL) {
			continue;
		}
		if ((uintptr_t)head < (uintptr_t)b ||
		    (uintptr_t)head >= (uintptr_t)b + REGION_SIZE) {
			_exit(21);
		}
	}
	_exit(0);
}

// Runs phase_func in a forked child so a real fault is reported as a
// descriptive test failure instead of taking down this test binary.
static int
run_child(void (*phase_func)(void)) {
	pid_t pid = fork();
	if (pid < 0) {
		return -1;
	}
	if (pid == 0) {
		phase_func();
		_exit(0);
	}
	int status = 0;
	if (waitpid(pid, &status, 0) < 0) {
		return -1;
	}
	if (WIFSIGNALED(status)) {
		return 100 + WTERMSIG(status);
	}
	return WEXITSTATUS(status);
}

// Verifies that value_table_free survives a rebase (positive control).
static int
test_value_table_survives_rebase(void) {
	int rc = run_child(phase_table_free);
	TEST_ASSERT_EQUAL(rc, 0, "value_table_free must survive a rebase");
	return 0;
}

// Verifies that value_registry_fini survives a rebase once memory_context
// is stored as an offset pointer.
static int
test_value_registry_survives_rebase(void) {
	int rc = run_child(phase_registry_fini);
	TEST_ASSERT_EQUAL(rc, 0, "value_registry_fini must survive a rebase");
	return 0;
}

// Verifies that finalising a rebased registry with the old mapping still
// live leaves every free-list head resolving inside the new mapping.
static int
test_value_registry_rebase_no_wild_free_list(void) {
	int rc = run_child(phase_registry_fini_wild);
	TEST_ASSERT_EQUAL(
		rc,
		0,
		"a free-list head resolved outside the mapping after rebase"
	);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	// Prove the rebase mechanism itself is sound before trusting it as a
	// test oracle: two ordinary mappings of the same fd must land at
	// different bases. A platform where this fails cannot run this test.
	int probe_fd = make_memfd();
	if (probe_fd < 0) {
		LOG(ERROR, "memfd_create failed, skipping");
		return 77;
	}
	struct arena *probe_a, *probe_b;
	if (map_pair(probe_fd, &probe_a, &probe_b)) {
		LOG(ERROR, "could not obtain two distinct mappings, skipping");
		close(probe_fd);
		return 77;
	}
	munmap(probe_a, REGION_SIZE);
	munmap(probe_b, REGION_SIZE);
	close(probe_fd);

	if (test_value_table_survives_rebase() != 0) {
		LOG(ERROR, "test_value_table_survives_rebase failed");
		return -1;
	}
	if (test_value_registry_survives_rebase() != 0) {
		LOG(ERROR, "test_value_registry_survives_rebase failed");
		return -1;
	}
	if (test_value_registry_rebase_no_wild_free_list() != 0) {
		LOG(ERROR,
		    "test_value_registry_rebase_no_wild_free_list failed");
		return -1;
	}

	LOG(INFO, "registry_rebase tests: OK");
	return 0;
}

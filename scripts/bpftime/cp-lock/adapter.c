// clang-format off
//go:build cp_lock_bpftime
// clang-format on

#include "adapter.h"
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

static int
snapshot(
	const struct cp_lock_site_stats *source,
	struct cp_lock_site_stats *copy,
	char *error
) {
	for (int attempt = 0; attempt < 128; attempt++) {
		uint64_t generation =
			__atomic_load_n(&source->generation, __ATOMIC_SEQ_CST);
		if (__atomic_load_n(&source->writer, __ATOMIC_SEQ_CST)) {
			continue;
		}

		__u64 *destination = (__u64 *)copy;
		const __u64 *values = (const __u64 *)source;
		for (size_t idx = 0; idx < sizeof(*copy) / sizeof(__u64);
		     idx++) {
			destination[idx] =
				__atomic_load_n(&values[idx], __ATOMIC_RELAXED);
		}

		__atomic_thread_fence(__ATOMIC_SEQ_CST);
		if (!__atomic_load_n(&source->writer, __ATOMIC_SEQ_CST) &&
		    generation == __atomic_load_n(
					  &source->generation, __ATOMIC_SEQ_CST
				  )) {
			uint64_t total = 0;
			for (size_t idx = 0; idx < CP_LOCK_HIST_BUCKETS;
			     idx++) {
				total += copy->hist[idx];
			}

			if (total != copy->count ||
			    copy->wait_max_ns > copy->wait_sum_ns ||
			    copy->hold_max_ns > copy->hold_sum_ns) {
				snprintf(error, 512, "incoherent statistics");
				return -1;
			}
			return 0;
		}
	}

	snprintf(error, 512, "statistics busy; retry report");
	return -1;
}

int
cp_lock_snapshot(
	const char *name,
	uint64_t inode,
	const uint64_t *offsets,
	struct cp_lock_site_stats *rows,
	struct cp_lock_counters *counts,
	char *error
) {
	int fd = shm_open(name, O_RDONLY | O_CLOEXEC, 0);
	if (fd < 0) {
		snprintf(error, 512, "open session: %s", strerror(errno));
		return -1;
	}

	struct stat status;
	if (fstat(fd, &status) || status.st_size <= 0 ||
	    (uint64_t)status.st_size > SIZE_MAX ||
	    (uint64_t)status.st_ino != inode) {
		snprintf(error, 512, "invalid session identity or size");
		close(fd);
		return -1;
	}

	size_t size = status.st_size;
	const size_t lengths[] = {
		sizeof(*rows) * CP_LOCK_MAX_SITES, sizeof(*counts)
	};
	const size_t alignments[] = {
		_Alignof(struct cp_lock_site_stats),
		_Alignof(struct cp_lock_counters)
	};

	for (size_t idx = 0; idx < 2; idx++) {
		if (offsets[idx] > size || lengths[idx] > size - offsets[idx] ||
		    offsets[idx] % alignments[idx]) {
			snprintf(error, 512, "invalid session payload extent");
			close(fd);
			return -1;
		}
	}

	if (offsets[0] < offsets[1] + lengths[1] &&
	    offsets[1] < offsets[0] + lengths[0]) {
		snprintf(error, 512, "overlapping session payload extents");
		close(fd);
		return -1;
	}

	void *memory = mmap(NULL, size, PROT_READ, MAP_SHARED, fd, 0);
	int saved_errno = errno;
	close(fd);
	if (memory == MAP_FAILED) {
		snprintf(error, 512, "map session: %s", strerror(saved_errno));
		return -1;
	}

	const struct cp_lock_site_stats *sites =
		(const void *)((const char *)memory + offsets[0]);
	int result = 0;
	for (size_t idx = 0; idx < CP_LOCK_MAX_SITES; idx++) {
		if (snapshot(&sites[idx], &rows[idx], error)) {
			result = -1;
			break;
		}
	}

	if (!result) {
		const struct cp_lock_counters *source =
			(const void *)((const char *)memory + offsets[1]);
		counts->drops =
			__atomic_load_n(&source->drops, __ATOMIC_RELAXED);
		counts->overflow =
			__atomic_load_n(&source->overflow, __ATOMIC_RELAXED);
	}

	if (munmap(memory, size) && !result) {
		snprintf(error, 512, "unmap session: %s", strerror(errno));
		result = -1;
	}

	return result;
}

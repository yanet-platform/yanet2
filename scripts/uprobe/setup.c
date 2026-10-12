// clang-format off
//go:build ignore
// clang-format on

#include "cp_lock.h"
#include <bpf/libbpf.h>
#include <fcntl.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/sysmacros.h>
#include <unistd.h>

static int
payload_offset(
	struct bpf_object *object,
	const struct stat *segment,
	const char *name,
	uint32_t value_size,
	uint32_t entries,
	uint64_t *offset
) {
	struct bpf_map *map = bpf_object__find_map_by_name(object, name);
	if (!map || bpf_map__type(map) != BPF_MAP_TYPE_ARRAY ||
	    bpf_map__key_size(map) != sizeof(uint32_t) ||
	    bpf_map__value_size(map) != value_size ||
	    bpf_map__max_entries(map) != entries) {
		return -1;
	}

	int fd = bpf_map__fd(map);
	size_t length = (size_t)value_size * entries;
	void *data = mmap(NULL, length, PROT_READ, MAP_SHARED, fd, 0);
	if (data == MAP_FAILED || (uintptr_t)data % _Alignof(__u64)) {
		return -1;
	}

	FILE *maps = fopen("/proc/self/maps", "r");
	if (!maps) {
		return -1;
	}

	char *line = NULL;
	size_t capacity = 0;
	int result = -1;

	while (getline(&line, &capacity, maps) >= 0) {
		unsigned long start, end;
		unsigned long long file_offset, inode;
		unsigned device_major, device_minor;
		char permissions[5];

		if (sscanf(line,
			   "%lx-%lx %4s %llx %x:%x %llu",
			   &start,
			   &end,
			   permissions,
			   &file_offset,
			   &device_major,
			   &device_minor,
			   &inode) != 7 ||
		    start > (uintptr_t)data || end <= (uintptr_t)data ||
		    length > end - (uintptr_t)data ||
		    inode != (uint64_t)segment->st_ino ||
		    makedev(device_major, device_minor) != segment->st_dev) {
			continue;
		}

		uint64_t displacement = (uintptr_t)data - start;
		if (file_offset > UINT64_MAX - displacement) {
			break;
		}

		*offset = file_offset + displacement;
		if (*offset > (uint64_t)segment->st_size ||
		    length > (uint64_t)segment->st_size - *offset ||
		    *offset % _Alignof(__u64)) {
			break;
		}

		result = 0;
		break;
	}

	free(line);
	fclose(maps);
	return result;
}

int
main(int argc, char **argv) {
	if (argc == 2 && !strcmp(argv[1], "--empty")) {
		unsetenv("BPFTIME_USED");
		// A fresh loader resets the exclusively owned handler registry.
		close(-1);
		const char *used = getenv("BPFTIME_USED");
		fflush(NULL);
		_exit(used && !strcmp(used, "1") ? 0 : 1);
	}

	if (argc != 7) {
		return 2;
	}

	struct bpf_object *object = bpf_object__open_file(argv[1], NULL);
	if (libbpf_get_error(object) || bpf_object__load(object)) {
		return 1;
	}

	const char *name = getenv("BPFTIME_GLOBAL_SHM_NAME");
	if (!name || !getenv("BPFTIME_USED")) {
		return 1;
	}

	int fd = shm_open(name, O_RDONLY | O_CLOEXEC, 0);
	struct stat segment;
	if (fd < 0) {
		return 1;
	}

	int invalid = fstat(fd, &segment) || segment.st_size <= 0;
	close(fd);

	uint64_t offsets[2];
	if (invalid ||
	    payload_offset(
		    object,
		    &segment,
		    "cp_lock_sites",
		    sizeof(struct cp_lock_site_stats),
		    CP_LOCK_MAX_SITES,
		    &offsets[0]
	    ) ||
	    payload_offset(
		    object,
		    &segment,
		    "cp_lock_counts",
		    sizeof(struct cp_lock_counters),
		    1,
		    &offsets[1]
	    )) {
		return 1;
	}

	const char *entries[] = {
		"cp_lock_on_lock_entry",
		"cp_lock_on_try_lock_entry",
		"cp_lock_on_unlock_entry"
	};
	const char *returns[] = {
		"cp_lock_on_lock_return",
		"cp_lock_on_try_lock_return",
		"cp_lock_on_unlock_return"
	};

	for (int idx = 0; idx < 3; idx++) {
		for (int returning = 0; returning < 2; returning++) {
			struct bpf_program *program =
				bpf_object__find_program_by_name(
					object,
					returning ? returns[idx] : entries[idx]
				);
			if (!program) {
				return 1;
			}

			struct bpf_link *link = bpf_program__attach_uprobe(
				program,
				returning,
				atoi(argv[2]),
				argv[3],
				strtoull(argv[idx + 4], NULL, 0)
			);
			if (libbpf_get_error(link) || !link) {
				return 1;
			}
		}
	}

	printf("CP_LOCK_ARRAY_OFFSETS %" PRIu64 " %" PRIu64 "\n",
	       offsets[0],
	       offsets[1]);

	// Shared handlers survive the loader handoff to the agent.
	fflush(NULL);
	_exit(0);
}

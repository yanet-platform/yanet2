#pragma once

#include <linux/magic.h>
#include <sys/statfs.h>
#include <unistd.h>

// Checks whether the file backed by given file descriptor is mounted on
// hugepages.
//
// Returns 1 if the file is on hugepages, 0 if not, and -1 on error.
static inline int
is_file_on_hugepages_fs(int fd) {
	struct statfs fs_stat;

	if (fstatfs(fd, &fs_stat) == -1) {
		return -1;
	}

	return (fs_stat.f_type == HUGETLBFS_MAGIC) ? 1 : 0;
}

// Returns the fault granularity (bytes) of the filesystem backing fd:
// the huge page size for hugetlbfs, otherwise the base page size.
// Returns -1 on error.
static inline long
file_page_size(int fd) {
	struct statfs fs_stat;
	if (fstatfs(fd, &fs_stat) == -1) {
		return -1;
	}
	if (fs_stat.f_type == HUGETLBFS_MAGIC) {
		return fs_stat.f_bsize;
	}
	return sysconf(_SC_PAGESIZE);
}

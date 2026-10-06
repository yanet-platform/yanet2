#pragma once

#include <linux/types.h>

#define CP_LOCK_SCHEMA 3
#ifndef CP_LOCK_MAX_SITES
#define CP_LOCK_MAX_SITES 1024
#endif
#define CP_LOCK_MAX_THREADS 4096
#define CP_LOCK_HIST_BUCKETS 64
#define CP_LOCK_RETRIES 16

struct cp_lock_site_stats {
	__u64 writer, generation, address;
	__u64 count, wait_sum_ns, wait_max_ns, hold_sum_ns, hold_max_ns;
	__u64 fail_count, hist[CP_LOCK_HIST_BUCKETS];
};

struct cp_lock_state {
	__u64 start_ns, acquired_ns, end_ns, ret;
};

struct cp_lock_counters {
	__u64 drops, overflow;
};

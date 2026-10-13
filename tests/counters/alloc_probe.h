#pragma once
#include <stddef.h>

struct probe_snapshot {
	size_t allocations;
	size_t live_count;
	size_t live_bytes;
	size_t noise_completed;
	size_t suppressed;
	int incomplete;
	int hook_error;
};
struct probe_snapshot
probe_snapshot_load(void);
void
probe_arm_read(void);
void
probe_arm_noise(void);
void
probe_foreign_noise(void);
void
probe_arm_retention(void);
void
probe_arm_overflow(void);
int
probe_release_retained(int other_thread);
int
probe_reset_controls(void);

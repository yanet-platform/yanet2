// clang-format off
//go:build ignore
// clang-format on

#include "cp_lock.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <linux/bpf.h>
#include <linux/ptrace.h>

char LICENSE[] SEC("license") = "GPL";

#define MAP(name, kind, bound, key_type, value_type)                           \
	struct {                                                               \
		__uint(type, kind);                                            \
		__uint(max_entries, bound);                                    \
		__type(key, key_type);                                         \
		__type(value, value_type);                                     \
	} name SEC(".maps")

MAP(cp_lock_sites,
    BPF_MAP_TYPE_ARRAY,
    CP_LOCK_MAX_SITES,
    __u32,
    struct cp_lock_site_stats);
MAP(cp_lock_state,
    BPF_MAP_TYPE_HASH,
    CP_LOCK_MAX_THREADS,
    __u64,
    struct cp_lock_state);
MAP(cp_lock_counts, BPF_MAP_TYPE_ARRAY, 1, __u32, struct cp_lock_counters);

static __always_inline void
dropped(int overflow) {
	__u32 zero = 0;
	struct cp_lock_counters *counts =
		bpf_map_lookup_elem(&cp_lock_counts, &zero);

	if (counts) {
		__sync_fetch_and_add(
			overflow ? &counts->overflow : &counts->drops, 1
		);
	}
}

// Writer ownership covers the complete record; exhausted retries drop a sample.
static __always_inline int
own(struct cp_lock_site_stats *stats) {
	for (int idx = 0; idx < CP_LOCK_RETRIES; idx++) {
		if (__sync_fetch_and_or(&stats->writer, 1) == 0) {
			return 1;
		}
	}

	dropped(0);
	return 0;
}

static __always_inline void
release(struct cp_lock_site_stats *stats) {
	__sync_fetch_and_add(&stats->generation, 1);
	__sync_fetch_and_sub(&stats->writer, 1);
}

// A busy candidate drops the sample to preserve unique site ownership.
static __always_inline struct cp_lock_site_stats *
site(__u64 address) {
	__u32 key = ((address >> 4) ^ (address >> 32)) % CP_LOCK_MAX_SITES;

	for (int idx = 0; idx < CP_LOCK_MAX_SITES; idx++) {
		struct cp_lock_site_stats *stats =
			bpf_map_lookup_elem(&cp_lock_sites, &key);

		if (!stats || !own(stats)) {
			return 0;
		}
		if (!stats->address || stats->address == address) {
			stats->address = address;
			return stats;
		}

		release(stats);
		key = (key + 1) % CP_LOCK_MAX_SITES;
	}

	dropped(1);
	return 0;
}

static __always_inline int
entry(struct pt_regs *ctx) {
	__u64 thread = bpf_get_current_pid_tgid();
	if (bpf_map_lookup_elem(&cp_lock_state, &thread)) {
		dropped(0);
	}

	struct cp_lock_state state = {.start_ns = bpf_ktime_get_ns()};
	// A userspace entry hook retains its live caller return slot on stack.
	state.ret = *(const __u64 *)PT_REGS_SP(ctx);

	if (bpf_map_update_elem(&cp_lock_state, &thread, &state, BPF_ANY)) {
		bpf_map_delete_elem(&cp_lock_state, &thread);
		dropped(0);
	}

	return 0;
}

SEC("uprobe/cp_config_lock")
int
cp_lock_on_lock_entry(struct pt_regs *ctx) {
	return entry(ctx);
}

SEC("uprobe/cp_config_try_lock")
int
cp_lock_on_try_lock_entry(struct pt_regs *ctx) {
	return entry(ctx);
}

static __always_inline int
acquired(int success) {
	__u64 thread = bpf_get_current_pid_tgid();
	struct cp_lock_state *state =
		bpf_map_lookup_elem(&cp_lock_state, &thread);

	if (!state) {
		dropped(0);
		return 0;
	}

	if (success) {
		state->acquired_ns = bpf_ktime_get_ns();
	} else {
		struct cp_lock_site_stats *stats = site(state->ret);
		if (stats) {
			stats->fail_count++;
			release(stats);
		}
		bpf_map_delete_elem(&cp_lock_state, &thread);
	}

	return 0;
}

SEC("uretprobe/cp_config_lock")
int
cp_lock_on_lock_return(struct pt_regs *ctx) {
	(void)ctx;
	return acquired(1);
}

SEC("uretprobe/cp_config_try_lock")
int
cp_lock_on_try_lock_return(struct pt_regs *ctx) {
	return acquired((__u8)PT_REGS_RC(ctx));
}

SEC("uprobe/cp_config_unlock")
int
cp_lock_on_unlock_entry(struct pt_regs *ctx) {
	(void)ctx;
	__u64 thread = bpf_get_current_pid_tgid();
	struct cp_lock_state *state =
		bpf_map_lookup_elem(&cp_lock_state, &thread);

	if (state) {
		state->end_ns = bpf_ktime_get_ns();
	} else {
		dropped(0);
	}

	return 0;
}

SEC("uretprobe/cp_config_unlock")
int
cp_lock_on_unlock_return(struct pt_regs *ctx) {
	(void)ctx;
	__u64 thread = bpf_get_current_pid_tgid();
	struct cp_lock_state *state =
		bpf_map_lookup_elem(&cp_lock_state, &thread);

	if (!state) {
		return 0;
	}

	if (!state->acquired_ns || state->acquired_ns < state->start_ns ||
	    state->end_ns < state->acquired_ns) {
		dropped(0);
	} else {
		struct cp_lock_site_stats *stats = site(state->ret);
		if (stats) {
			__u64 wait = state->acquired_ns - state->start_ns,
			      hold = state->end_ns - state->acquired_ns;

			stats->count++;
			stats->wait_sum_ns += wait;
			stats->hold_sum_ns += hold;

			if (wait > stats->wait_max_ns) {
				stats->wait_max_ns = wait;
			}
			if (hold > stats->hold_max_ns) {
				stats->hold_max_ns = hold;
			}

			__u64 duration = hold;
			__u32 bucket = 0;

			for (int idx = 0; idx < 63; idx++) {
				if (duration < 2) {
					break;
				}
				duration >>= 1;
				bucket++;
			}
			stats->hist[bucket]++;
			release(stats);
		}
	}

	bpf_map_delete_elem(&cp_lock_state, &thread);
	return 0;
}

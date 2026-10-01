#pragma once

#include <stdint.h>
#include <time.h>

#include "common/cache.h"

/*
 * Instrumentation of the controlplane configuration lock.
 *
 * Every acquisition through cp_config_lock()/cp_config_try_lock() is
 * attributed to the API entry point that performed it (the site) and
 * accounts two durations:
 *   - wait: time spent trying to acquire, before the lock was granted;
 *   - hold: time the lock stayed held, until cp_config_unlock().
 *
 * The counters live in the shared-memory cp_config zone, so every
 * attached process contributes to and reads the same table. They are
 * advisory diagnostics: updated with relaxed atomics, with no ordering
 * guarantees between the fields of one site.
 */

// The API entry point a lock acquisition is attributed to.
//
// The values are written into shared memory and read back through other
// processes' structs, so they must stay stable; new sites are appended
// before CP_CONFIG_LOCK_SITE_COUNT only.
enum cp_config_lock_site {
	// Acquisitions without an explicit tag: tests, internal helpers.
	CP_CONFIG_LOCK_SITE_OTHER = 0,
	// The cp_config_update/delete family.
	CP_CONFIG_LOCK_SITE_UPDATE_MODULES,
	CP_CONFIG_LOCK_SITE_DELETE_MODULE,
	CP_CONFIG_LOCK_SITE_UPDATE_FUNCTIONS,
	CP_CONFIG_LOCK_SITE_DELETE_FUNCTION,
	CP_CONFIG_LOCK_SITE_UPDATE_PIPELINES,
	CP_CONFIG_LOCK_SITE_DELETE_PIPELINE,
	CP_CONFIG_LOCK_SITE_UPDATE_DEVICES,
	CP_CONFIG_LOCK_SITE_DELETE_DEVICE,
	CP_CONFIG_LOCK_SITE_UPDATE_OBJECTS,
	CP_CONFIG_LOCK_SITE_DELETE_OBJECT,
	// Agent lifecycle.
	CP_CONFIG_LOCK_SITE_AGENT_ATTACH,
	CP_CONFIG_LOCK_SITE_AGENT_MEMORY_LIMIT,
	CP_CONFIG_LOCK_SITE_AGENT_EXTEND,
	CP_CONFIG_LOCK_SITE_SHM_EXTEND_AGENT,
	CP_CONFIG_LOCK_SITE_AGENT_FREE_UNUSED,
	CP_CONFIG_LOCK_SITE_ITEM_TRY_DESTROY,
	// Reads.
	CP_CONFIG_LOCK_SITE_GET_MODULES,
	CP_CONFIG_LOCK_SITE_GET_FUNCTIONS,
	CP_CONFIG_LOCK_SITE_GET_PIPELINES,
	CP_CONFIG_LOCK_SITE_GET_DEVICES,
	CP_CONFIG_LOCK_SITE_GET_AGENTS,
	CP_CONFIG_LOCK_SITE_GET_COUNTERS,
	CP_CONFIG_LOCK_SITE_GET_VLAN,
	CP_CONFIG_LOCK_SITE_ROUTE_SNAPSHOT_OPEN,
	CP_CONFIG_LOCK_SITE_ROUTE_SNAPSHOT_CLOSE,
	// Zone bootstrap and dataplane system-agent setup.
	CP_CONFIG_LOCK_SITE_DP_INIT,
	CP_CONFIG_LOCK_SITE_COUNT
};

// Short, stable name of each site for dumps; indexed by the enum above.
extern const char *const cp_config_lock_site_names[CP_CONFIG_LOCK_SITE_COUNT];

// Per-site counters.
//
// Deliberately free of alignment requirements beyond the natural
// uint64_t ones: struct cp_config is placed at an offset the dataplane
// configuration picks, which is only page-aligned as a whole, so a
// cache-line-aligned member would put the struct at a misaligned
// address for valid configurations. Adjacent sites may share a cache
// line, which only costs the recording path — it runs after the lock is
// released — some coherence traffic between concurrently accounting
// processes.
struct cp_config_lock_site_stats {
	uint64_t acquisitions;
	uint64_t wait_ns;
	uint64_t wait_max_ns;
	uint64_t hold_ns;
	uint64_t hold_max_ns;
};

// The whole table, embedded in struct cp_config.
struct cp_config_lock_stats {
	struct cp_config_lock_site_stats sites[CP_CONFIG_LOCK_SITE_COUNT];
};

// Monotonic clock in nanoseconds; the instrumentation's time base.
static inline uint64_t
cp_config_lock_now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Raise a relaxed maximum: retry while the candidate stays larger than
// the published value, giving up once it does not.
static inline void
cp_config_lock_max_ns(uint64_t *field, uint64_t value) {
	uint64_t cur = __atomic_load_n(field, __ATOMIC_RELAXED);
	while (value > cur && !__atomic_compare_exchange_n(
				      field,
				      &cur,
				      value,
				      false,
				      __ATOMIC_RELAXED,
				      __ATOMIC_RELAXED
			      )) {
	}
}

// Account one completed acquisition into its site.
//
// Called after the lock is released, so the atomics never run inside a
// critical section and cannot extend the next waiter's wait.
static inline void
cp_config_lock_stats_record(
	struct cp_config_lock_stats *stats,
	enum cp_config_lock_site site,
	uint64_t wait_ns,
	uint64_t hold_ns
) {
	if ((unsigned int)site >= CP_CONFIG_LOCK_SITE_COUNT) {
		site = CP_CONFIG_LOCK_SITE_OTHER;
	}
	struct cp_config_lock_site_stats *slot = &stats->sites[site];
	__atomic_fetch_add(&slot->acquisitions, 1, __ATOMIC_RELAXED);
	__atomic_fetch_add(&slot->wait_ns, wait_ns, __ATOMIC_RELAXED);
	__atomic_fetch_add(&slot->hold_ns, hold_ns, __ATOMIC_RELAXED);
	cp_config_lock_max_ns(&slot->wait_max_ns, wait_ns);
	cp_config_lock_max_ns(&slot->hold_max_ns, hold_ns);
}

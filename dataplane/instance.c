#include "dataplane.h"

#include <pthread.h>
#include <sched.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include <time.h>

#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

// sched_yield iterations per poll cycle, before the fixed sleep. Sixteen
// yields cost a few microseconds of busy time on an idle core, which keeps
// the thread hot through the window right after a poll without turning
// the phase into a spin.
#define CONFIG_ASSIGNER_YIELD_ITERS 16

// Fixed nanosleep interval that closes each poll cycle. A request of 100
// microseconds sleeps roughly 150 on a default-slack kernel, so the full
// cycle lands near 160 microseconds: a published generation reaches the
// workers well inside a millisecond while the idle thread burns only a
// few percent of one unpinned core.
#define CONFIG_ASSIGNER_SLEEP_NS UINT64_C(100000)

// Process-wide stop request for the assigner threads.
//
// The dataplane starts and stops exactly once, and every instance's
// assigner is stopped together, so one flag serves them all. Stored with
// release ordering before the joins; loaded with acquire ordering in the
// poll loops.
static bool config_assigner_stop;

static void *
config_assigner_thread(void *arg) {
	struct dataplane_instance *instance = (struct dataplane_instance *)arg;
	struct dp_config *dp_config = instance->dp_config;
	struct cp_config *cp_config = instance->cp_config;

	static const struct timespec sleep_ts = {
		.tv_sec = (time_t)(CONFIG_ASSIGNER_SLEEP_NS / 1000000000ULL),
		.tv_nsec = (long)(CONFIG_ASSIGNER_SLEEP_NS % 1000000000ULL),
	};

	// Nothing is assigned yet, so the first pass also delivers the
	// generation published before the thread started, bootstrap state
	// included.
	struct cp_config_gen *last_assigned = NULL;

	for (;;) {
		struct cp_config_gen *published =
			ATOMIC_ADDR_OF(&cp_config->cp_config_gen);
		if (published != last_assigned) {
			dp_config_assign_worker_ectxs(dp_config, published);
			last_assigned = published;
		}

		if (__atomic_load_n(&config_assigner_stop, __ATOMIC_ACQUIRE)) {
			break;
		}

		for (unsigned iters = 0; iters < CONFIG_ASSIGNER_YIELD_ITERS;
		     ++iters) {
			if (__atomic_load_n(
				    &config_assigner_stop, __ATOMIC_ACQUIRE
			    )) {
				break;
			}
			sched_yield();
		}
		nanosleep(&sleep_ts, NULL);
	}

	return NULL;
}

int
dataplane_instance_config_assigner_start(struct dataplane_instance *instance) {
	int rc = pthread_create(
		&instance->config_assigner_thread,
		NULL,
		config_assigner_thread,
		instance
	);
	if (rc != 0) {
		LOG(ERROR,
		    "failed to create config assigner thread: %s",
		    strerror(rc));
		return -1;
	}
	instance->config_assigner_started = true;
	return 0;
}

void
dataplane_instance_config_assigner_stop(struct dataplane_instance *instance) {
	if (!instance->config_assigner_started) {
		return;
	}

	__atomic_store_n(&config_assigner_stop, true, __ATOMIC_RELEASE);
	pthread_join(instance->config_assigner_thread, NULL);
	instance->config_assigner_started = false;
}

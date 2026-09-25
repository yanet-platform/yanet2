#include "alloc_probe.h"
#include "../../api/counter.h"
#include <errno.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

extern void *
__real_malloc(size_t size);
extern void *
__real_calloc(size_t count, size_t size);
extern char *
__real_strdup(const char *str);
extern void
__real_free(void *ptr);
extern struct counter_worker_set_list *
__real_yanet_get_counters_by_tags_per_worker(
	struct dp_config *config,
	const struct counter_tag *tags,
	size_t count,
	const struct counter_query *query,
	yanet_error **error
);

enum { RECORD_CAPACITY = 1024 };
static pthread_mutex_t mutex = PTHREAD_MUTEX_INITIALIZER;
static _Thread_local int admission;
static struct {
	void *pointer;
	size_t size;
} records[RECORD_CAPACITY];
static struct probe_snapshot snapshot;
static size_t capacity = RECORD_CAPACITY;
static int noise_armed;
static int retain_armed;
static int overflow_armed;
static int read_armed;
static void *retained;
static int suppress_free;

static void
track_alloc(void *ptr, size_t size) {
	if (!admission) {
		return;
	}
	pthread_mutex_lock(&mutex);
	++snapshot.allocations;
	if (ptr != NULL) {
		size_t idx;
		for (idx = 0; idx < capacity; ++idx) {
			if (records[idx].pointer == NULL) {
				records[idx].pointer = ptr;
				records[idx].size = size;
				++snapshot.live_count;
				snapshot.live_bytes += size;
				break;
			}
		}
		if (idx == capacity) {
			snapshot.incomplete = 1;
		}
	}
	pthread_mutex_unlock(&mutex);
}

void *
__wrap_malloc(size_t size) {
	void *ptr = __real_malloc(size);
	track_alloc(ptr, size);
	return ptr;
}
void *
__wrap_calloc(size_t count, size_t size) {
	void *ptr = __real_calloc(count, size);
	track_alloc(ptr, ptr == NULL ? 0 : count * size);
	return ptr;
}
char *
__wrap_strdup(const char *str) {
	char *ptr = __real_strdup(str);
	track_alloc(ptr, ptr == NULL ? 0 : strlen(str) + 1);
	return ptr;
}
void
__wrap_free(void *ptr) {
	if (ptr == NULL) {
		return;
	}
	pthread_mutex_lock(&mutex);
	if (ptr == retained && suppress_free) {
		suppress_free = 0;
		++snapshot.suppressed;
		pthread_mutex_unlock(&mutex);
		return;
	}
	for (size_t idx = 0; idx < RECORD_CAPACITY; ++idx) {
		if (records[idx].pointer == ptr) {
			--snapshot.live_count;
			snapshot.live_bytes -= records[idx].size;
			records[idx].pointer = NULL;
			break;
		}
	}
	pthread_mutex_unlock(&mutex);
	__real_free(ptr);
}

struct probe_snapshot
probe_snapshot_load(void) {
	pthread_mutex_lock(&mutex);
	struct probe_snapshot result = snapshot;
	pthread_mutex_unlock(&mutex);
	return result;
}

static void *
noise(void *unused) {
	(void)unused;
	int failed = 0;
	for (size_t idx = 0; idx < 100; ++idx) {
		void *first = malloc(31);
		void *second = calloc(3, 17);
		char *third = strdup("foreign allocation");
		failed |= first == NULL || second == NULL || third == NULL;
		free(first);
		free(second);
		free(third);
	}
	/* This pointer never passes through the admission wrapper. */
	void *unknown = __real_malloc(23);
	failed |= unknown == NULL;
	free(unknown);
	free(NULL);
	pthread_mutex_lock(&mutex);
	++snapshot.noise_completed;
	if (failed) {
		snapshot.hook_error = ENOMEM;
	}
	pthread_mutex_unlock(&mutex);
	return NULL;
}

static int
run_thread(void *(*function)(void *)) {
	pthread_t thread;
	int result = pthread_create(&thread, NULL, function, NULL);
	if (result == 0) {
		result = pthread_join(thread, NULL);
	}
	return result;
}

void
probe_arm_read(void) {
	pthread_mutex_lock(&mutex);
	read_armed = 1;
	pthread_mutex_unlock(&mutex);
}
void
probe_arm_noise(void) {
	pthread_mutex_lock(&mutex);
	noise_armed = 1;
	pthread_mutex_unlock(&mutex);
}
void
probe_foreign_noise(void) {
	noise(NULL);
}
void
probe_arm_retention(void) {
	pthread_mutex_lock(&mutex);
	retain_armed = 1;
	pthread_mutex_unlock(&mutex);
}
void
probe_arm_overflow(void) {
	pthread_mutex_lock(&mutex);
	overflow_armed = 1;
	pthread_mutex_unlock(&mutex);
}

static void *
release_retained(void *unused) {
	(void)unused;
	pthread_mutex_lock(&mutex);
	void *ptr = retained;
	retained = NULL;
	suppress_free = 0;
	pthread_mutex_unlock(&mutex);
	free(ptr);
	return NULL;
}
int
probe_release_retained(int other_thread) {
	if (other_thread) {
		return run_thread(release_retained);
	}
	release_retained(NULL);
	return 0;
}

int
probe_reset_controls(void) {
	pthread_mutex_lock(&mutex);
	int result = EBUSY;
	if (snapshot.live_count == 0 && retained == NULL) {
		noise_armed = retain_armed = overflow_armed = read_armed = 0;
		snapshot.incomplete = snapshot.hook_error = 0;
		capacity = RECORD_CAPACITY;
		result = 0;
	}
	pthread_mutex_unlock(&mutex);
	return result;
}

struct counter_worker_set_list *
__wrap_yanet_get_counters_by_tags_per_worker(
	struct dp_config *config,
	const struct counter_tag *tags,
	size_t count,
	const struct counter_query *query,
	yanet_error **error
) {
	pthread_mutex_lock(&mutex);
	int noisy = noise_armed;
	int keep = retain_armed;
	int overflow = overflow_armed;
	int measure = read_armed;
	read_armed = 0;
	noise_armed = retain_armed = overflow_armed = 0;
	if (overflow) {
		capacity = 1;
	}
	pthread_mutex_unlock(&mutex);
	if (noisy) {
		noise(NULL);
	}
	int previous = admission;
	admission = measure;
	if (noisy) {
		int result = run_thread(noise);
		if (result != 0) {
			pthread_mutex_lock(&mutex);
			snapshot.hook_error = result;
			pthread_mutex_unlock(&mutex);
		}
	}
	struct counter_worker_set_list *sets =
		__real_yanet_get_counters_by_tags_per_worker(
			config, tags, count, query, error
		);
	admission = previous;
	/* Check restored admission on the same C thread, including errors. */
	if (noisy) {
		noise(NULL);
	}
	pthread_mutex_lock(&mutex);
	capacity = RECORD_CAPACITY;
	if (keep && sets != NULL) {
		retained = sets;
		suppress_free = 1;
	}
	pthread_mutex_unlock(&mutex);
	return sets;
}

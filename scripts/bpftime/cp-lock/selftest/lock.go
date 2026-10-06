//go:build cp_lock_bpftime

package selftest

/*
#cgo CFLAGS: -I../../../..
#cgo LDFLAGS: ${SRCDIR}/zone.o -pthread
#include <pthread.h>
#include <stdatomic.h>
#include <sys/prctl.h>
#include <time.h>
#include "lib/controlplane/config/zone.h"

static void
permit_injection(void) {
	prctl(PR_SET_PTRACER, PR_SET_PTRACER_ANY, 0, 0, 0);
}

static struct cp_config configuration;
static _Atomic int ready;
static clockid_t waiter_clock;
static struct timespec wait_start;

static void
delay(long nanoseconds) {
	struct timespec duration = {0, nanoseconds};
	nanosleep(&duration, NULL);
}

__attribute__((noinline)) void
cp_lock_test_first(void) {
	cp_config_lock(&configuration);
	delay(1000000);
	cp_config_unlock(&configuration);
}

__attribute__((noinline)) void
cp_lock_test_second(void) {
	cp_config_lock(&configuration);
	delay(1000000);
	cp_config_unlock(&configuration);
}

__attribute__((noinline)) void
cp_lock_test_try(void) {
	if (cp_config_try_lock(&configuration)) {
		cp_config_unlock(&configuration);
	}
}

static void *
first_worker(void *value) {
	for (long idx = 0; idx < (long)value; idx++) {
		cp_lock_test_first();
	}

	return NULL;
}

static void *
second_worker(void *value) {
	for (long idx = 0; idx < (long)value; idx++) {
		cp_lock_test_second();
	}

	return NULL;
}

static void *
holder(void *value) {
	(void)value;
	cp_config_lock(&configuration);
	atomic_store(&ready, 1);
	while (atomic_load(&ready) != 2) {
		delay(10000);
	}

	// Keep the lock until the waiter has spent 20 ms executing its spin.
	struct timespec current;

	do {
		delay(10000);
		clock_gettime(waiter_clock, &current);
	} while ((current.tv_sec - wait_start.tv_sec) * 1000000000L +
		 current.tv_nsec - wait_start.tv_nsec < 20000000L);

	cp_config_unlock(&configuration);
	return NULL;
}

static void
batch(long count) {
	pthread_t first, second;

	pthread_create(&first, NULL, first_worker, (void *)count);
	pthread_create(&second, NULL, second_worker, (void *)count);
	pthread_join(first, NULL);
	pthread_join(second, NULL);
}

static void
failed(void) {
	pthread_t thread;

	atomic_store(&ready, 0);
	pthread_create(&thread, NULL, holder, NULL);
	while (!atomic_load(&ready)) {
		delay(10000);
	}

	cp_lock_test_try();
	pthread_getcpuclockid(pthread_self(), &waiter_clock);
	clock_gettime(waiter_clock, &wait_start);
	atomic_store(&ready, 2);
	cp_lock_test_second();
	pthread_join(thread, NULL);
}
*/
import "C"

func PermitInjection() { C.permit_injection() }

func Batch(count int) { C.batch(C.long(count)) }

func Failed() { C.failed() }

func Try() { C.cp_lock_test_try() }

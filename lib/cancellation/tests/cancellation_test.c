/*
 * Pins the cancellation token contract.
 *
 * A token is raised from any thread but observed through a thread-local
 * binding: a thread with no binding is never cancelled, a bound thread
 * sees a raise made elsewhere, the raising thread does not, and a nested
 * binding restores the one it replaced.
 */

#include "common/test_assert.h"

#include "lib/cancellation/cancellation.h"

#include "lib/logging/log.h"

#include <pthread.h>
#include <stdbool.h>

struct fire_args {
	struct cancellation_token *token;
	bool raiser_saw_cancel;
};

// Raises the token of another thread, recording whether the raise makes
// the raising thread itself appear cancelled.
static void *
fire_thread(void *arg) {
	struct fire_args *args = arg;
	cancellation_token_fire(args->token);
	args->raiser_saw_cancel = cancellation_requested();
	return NULL;
}

// Verifies that a thread without a binding, or holding an unraised
// token, is not cancelled.
static int
run_unbound_test(void) {
	TEST_ASSERT(!cancellation_requested(), "cancelled with no binding");

	struct cancellation_token token = {0};
	TEST_ASSERT(
		cancellation_token_bind(&token) == NULL,
		"reported a binding that was never made"
	);
	TEST_ASSERT(!cancellation_requested(), "cancelled before any raise");

	// The scenarios below start from a thread that holds no binding.
	cancellation_token_bind(NULL);
	return TEST_SUCCESS;
}

// Verifies that a raise reaches the bound thread, leaves the raising
// thread alone, and stops being visible once the binding is dropped.
static int
run_cross_thread_fire_test(void) {
	struct cancellation_token token = {0};
	cancellation_token_bind(&token);

	struct fire_args args = {
		.token = &token,
		.raiser_saw_cancel = true,
	};
	pthread_t raiser;
	TEST_ASSERT_SUCCESS(
		pthread_create(&raiser, NULL, fire_thread, &args),
		"failed to start the raising thread"
	);
	TEST_ASSERT_SUCCESS(
		pthread_join(raiser, NULL), "failed to join the raising thread"
	);

	TEST_ASSERT(
		!args.raiser_saw_cancel,
		"the raise reached a thread that never bound the token"
	);
	TEST_ASSERT(cancellation_requested(), "the raise was not observed");

	cancellation_token_bind(NULL);
	TEST_ASSERT(!cancellation_requested(), "cancelled after unbinding");
	return TEST_SUCCESS;
}

// Verifies that a nested binding restores the one it replaced, leaving
// the outer operation armed.
static int
run_nested_bind_test(void) {
	struct cancellation_token outer = {0};
	struct cancellation_token inner = {0};

	cancellation_token_bind(&outer);
	struct cancellation_token *replaced = cancellation_token_bind(&inner);
	TEST_ASSERT(
		replaced == &outer, "the replaced binding was not reported"
	);

	cancellation_token_fire(&outer);
	TEST_ASSERT(
		!cancellation_requested(),
		"the outer raise reached the nested binding"
	);

	cancellation_token_bind(replaced);
	TEST_ASSERT(cancellation_requested(), "the outer binding was lost");

	cancellation_token_bind(NULL);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("error");

	if (run_unbound_test() != TEST_SUCCESS ||
	    run_cross_thread_fire_test() != TEST_SUCCESS ||
	    run_nested_bind_test() != TEST_SUCCESS) {
		return 1;
	}
	return 0;
}

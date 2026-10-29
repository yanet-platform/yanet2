/*
 * The agent name lease guards same-name attaches.
 *
 * While a live process holds a name, a same-name attach is refused
 * with failed precondition instead of superseding the holder's agent;
 * the refusal clears when the holder detaches cleanly or dies by any
 * means — the kernel releases the lease lock on exit, which a killed
 * child demonstrates. A segment with no backing path (an embedded
 * harness segment) has no lease, and same-name churn keeps working
 * there.
 */

#include "api/agent.h"

#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

#define LEASE_TEST_AGENT_MEMORY (4u * 1024u * 1024u)

// Points the harness segment at a backing path so the lease engages:
// the handle normally carries none, which is exactly the skip case.
static char scratch_dir[64];

static int
lease_shm_path(struct yanet_shm *shm, char *dir, size_t dir_size) {
	if (snprintf(
		    scratch_dir, sizeof(scratch_dir), "/tmp/yanet-lease-XXXXXX"
	    ) >= (int)sizeof(scratch_dir)) {
		return -1;
	}
	if (mkdtemp(scratch_dir) == NULL) {
		return -1;
	}
	snprintf(dir, dir_size, "%s/segment", scratch_dir);
	shm->path = strdup(dir);
	return (shm->path != NULL) ? 0 : -1;
}

// Remove exactly this run's scratch directory: the lease files below
// it, the lease directory, then the directory itself.
static void
lease_scratch_cleanup(void) {
	char path[128];
	if (snprintf(path, sizeof(path), "%s/yanet-agent-locks", scratch_dir) >=
	    (int)sizeof(path)) {
		return;
	}
	DIR *dir = opendir(path);
	if (dir != NULL) {
		struct dirent *entry;
		while ((entry = readdir(dir)) != NULL) {
			if (entry->d_name[0] == '.') {
				continue;
			}
			char *file = NULL;
			if (asprintf(&file, "%s/%s", path, entry->d_name) < 0) {
				continue;
			}
			unlink(file);
			free(file);
		}
		closedir(dir);
		rmdir(path);
	}
	rmdir(scratch_dir);
}

// Verifies a pathless segment keeps same-name churn: no lease, no
// refusal — the harness degradation every other test relies on.
static int
run_lease_skip_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *first =
		agent_attach(shm, 0, "pathless", LEASE_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		first, "first attach on a pathless segment failed"
	);

	struct agent *second =
		agent_attach(shm, 0, "pathless", LEASE_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		second, "same-name attach on a pathless segment must supersede"
	);

	// The superseded first agent was drained, so the superseding
	// attach's own sweep reclaimed it — only the surviving head is the
	// test's to destroy.
	agent_destroy(second);
	return TEST_SUCCESS;
}

// Verifies the refusal and the clean handoff: a live holder blocks the
// name, a detach opens it again without waiting.
static int
run_lease_refusal_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *holder =
		agent_attach(shm, 0, "leased", LEASE_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(holder, "the holder attach failed");

	yanet_error *refuse_err = NULL;
	struct agent *racer = agent_attach(
		shm, 0, "leased", LEASE_TEST_AGENT_MEMORY, &refuse_err
	);
	TEST_ASSERT_NULL(
		racer, "a same-name attach under a live holder must fail"
	);
	TEST_ASSERT_EQUAL(
		yanet_error_kind(refuse_err),
		YANET_ERROR_FAILED_PRECONDITION,
		"the refusal must carry the failed-precondition kind"
	);
	yanet_error_free(refuse_err);

	// The clean handoff: detaching drops the lease, and the very next
	// attach takes the name without waiting for anything.
	TEST_ASSERT_SUCCESS(agent_detach(holder), "the holder detach failed");

	err = NULL;
	struct agent *successor =
		agent_attach(shm, 0, "leased", LEASE_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(successor, "the attach after the handoff failed");

	agent_destroy(successor);
	return TEST_SUCCESS;
}

// Verifies the kernel-backed release: a killed holder's lease dies with
// it, and the attach that was refused succeeds without anyone cleaning
// anything up.
static int
run_lease_death_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *holder =
		agent_attach(shm, 0, "dying", LEASE_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(holder, "the holder attach failed");

	int ready_pipe[2];
	TEST_ASSERT_EQUAL(pipe(ready_pipe), 0, "pipe failed");

	pid_t child = fork();
	TEST_ASSERT(child >= 0, "fork failed");
	if (child == 0) {
		// The child inherits the segment and its path; its attach
		// holds a second name the parent then reaches for. The
		// harness memory is private to each side after the fork, so
		// only the real lease file crosses between them.
		close(ready_pipe[0]);
		yanet_error *child_err = NULL;
		struct agent *child_agent = agent_attach(
			shm, 0, "contested", LEASE_TEST_AGENT_MEMORY, &child_err
		);
		if (child_agent == NULL) {
			_exit(1);
		}
		char byte = 1;
		if (write(ready_pipe[1], &byte, 1) != 1) {
			_exit(1);
		}
		pause();
		_exit(0);
	}
	close(ready_pipe[1]);

	char byte = 0;
	bool ready = read(ready_pipe[0], &byte, 1) == 1;
	close(ready_pipe[0]);
	if (!ready) {
		kill(child, SIGKILL);
		waitpid(child, NULL, 0);
		TEST_ASSERT(ready, "the child never signalled its attach");
		return TEST_FAILED;
	}

	yanet_error *refuse_err = NULL;
	struct agent *racer = agent_attach(
		shm, 0, "contested", LEASE_TEST_AGENT_MEMORY, &refuse_err
	);
	if (racer != NULL ||
	    yanet_error_kind(refuse_err) != YANET_ERROR_FAILED_PRECONDITION) {
		kill(child, SIGKILL);
		waitpid(child, NULL, 0);
		yanet_error_free(refuse_err);
		TEST_ASSERT_NULL(
			racer,
			"the attach under the live child must have been refused"
		);
		return TEST_FAILED;
	}
	yanet_error_free(refuse_err);

	if (kill(child, SIGKILL) != 0) {
		TEST_ASSERT_EQUAL(
			kill(child, SIGKILL), 0, "killing the child failed"
		);
		return TEST_FAILED;
	}
	int status = 0;
	TEST_ASSERT_EQUAL(waitpid(child, &status, 0), child, "waitpid failed");

	err = NULL;
	struct agent *successor = agent_attach(
		shm, 0, "contested", LEASE_TEST_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		successor,
		"the attach must succeed once the holder died, with nothing "
		"cleaned up"
	);

	agent_destroy(successor);
	agent_destroy(holder);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("error");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 64u * 1024u * 1024u,
		.dp_memory = 4u * 1024u * 1024u,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);

	int res = run_lease_skip_test(shm);
	if (res == TEST_SUCCESS) {
		char dir[128];
		if (lease_shm_path(shm, dir, sizeof(dir)) != 0) {
			fprintf(stderr, "failed to give the segment a path\n");
			dataplane_ut_free(ut);
			return 1;
		}
		res = run_lease_refusal_test(shm);
		if (res == TEST_SUCCESS) {
			res = run_lease_death_test(shm);
		}
		free(shm->path);
		shm->path = NULL;
		lease_scratch_cleanup();
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}

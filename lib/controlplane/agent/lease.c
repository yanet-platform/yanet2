#include "lease.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>

#include "common/hash.h"
#include "common/spinlock.h"
#include "common/strutils.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/errors/errors.h"

// Directory provisioned beside the segment's storage file; the sticky
// bit keeps every attacher able to create a lease file while nobody
// can unlink another holder's.
#define AGENT_LEASE_DIR "yanet-agent-locks"

// One held lease per process: the registry only pairs an agent with
// its open lock file so the release path can drop both. Attaches are
// rare, so a flat array under a spinlock is enough.
struct agent_lease_entry {
	struct agent *agent;
	int fd;
};

static struct spinlock lease_lock;
static struct agent_lease_entry *leases;
static size_t lease_count;
static size_t lease_capacity;

static int
lease_path(
	uint32_t instance_idx,
	const char *shm_path,
	const char *agent_name,
	char *out,
	size_t out_size
) {
	// A storage path without a separator sits in the working
	// directory, so the lease directory is anchored there rather than
	// at the filesystem root.
	const char *dir = ".";
	size_t dir_len = 1;
	const char *sep = strrchr(shm_path, '/');
	if (sep != NULL) {
		dir = shm_path;
		dir_len = (size_t)(sep - shm_path);
	}

	uint64_t path_hash = wyhash64(0xa5a517b19d4d5b9dULL);
	for (const char *cursor = shm_path; *cursor != '\0'; ++cursor) {
		path_hash =
			wyhash64(path_hash ^ (uint64_t)(unsigned char)*cursor);
	}
	uint64_t name_hash = wyhash64(instance_idx + 1);
	for (const char *cursor = agent_name; *cursor != '\0'; ++cursor) {
		name_hash =
			wyhash64(name_hash ^ (uint64_t)(unsigned char)*cursor);
	}

	// The lease is keyed by the canonical segment path, so symlinked
	// spellings arbitrate together; bind mounts remain distinct paths
	// and must not be mixed for one segment.
	int written = snprintf(
		out,
		out_size,
		"%.*s/%s/%016llx-%016llx.lock",
		(int)dir_len,
		dir,
		AGENT_LEASE_DIR,
		(unsigned long long)path_hash,
		(unsigned long long)name_hash
	);
	return (written >= 0 && (size_t)written < out_size) ? 0 : -1;
}

int
agent_lease_acquire(
	struct agent *agent,
	const struct yanet_shm *shm,
	uint32_t instance_idx,
	const char *agent_name,
	yanet_error **err
) {
	if (shm == NULL || shm->path == NULL) {
		return 0;
	}

	char lock_path[PATH_MAX];
	if (lease_path(
		    instance_idx,
		    shm->path,
		    agent_name,
		    lock_path,
		    sizeof(lock_path)
	    ) != 0) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_RESOURCE_EXHAUSTED,
			"the agent lease path does not fit"
		);
		return -1;
	}

	// Cut the lease file out of the lock path for the directory
	// creation; the file name is always exactly one component.
	char *dir_end = strrchr(lock_path, '/');
	*dir_end = '\0';
	// The dataplane provisions the directory sticky; a racing first
	// attacher may also find it missing, so both create it.
	if (mkdir(lock_path, 01777) != 0 && errno != EEXIST) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_RESOURCE_EXHAUSTED,
			"failed to create the agent lease directory: %s",
			strerror(errno)
		);
		return -1;
	}
	// The creating umask would tighten a fresh directory, so pin the
	// intended mode; a pre-existing directory owned by another keeper
	// just keeps whatever it already has.
	(void)chmod(lock_path, 01777);
	*dir_end = '/';

	// The hash names are predictable: refuse a planted symlink
	// instead of locking through it.
	int fd =
		open(lock_path,
		     O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC | O_NOFOLLOW,
		     0666);
	if (fd < 0 && errno == EEXIST) {
		fd = open(lock_path, O_RDWR | O_CLOEXEC | O_NOFOLLOW);
	}
	if (fd < 0) {
		yanet_error_add_kind(
			err,
			YANET_ERROR_RESOURCE_EXHAUSTED,
			"failed to open the agent lease file: %s",
			strerror(errno)
		);
		return -1;
	}
	// A holder in another container may run under another user: strip
	// the creating umask so the next process can reopen the file.
	(void)fchmod(fd, 0666);

	if (flock(fd, LOCK_EX | LOCK_NB) != 0) {
		int saved = errno;
		close(fd);
		if (saved == EWOULDBLOCK) {
			yanet_error_add_kind(
				err,
				YANET_ERROR_FAILED_PRECONDITION,
				"agent \"%s\" is held by a live owner; retry "
				"after the previous process exits",
				agent_name
			);
			return -1;
		}
		yanet_error_add_kind(
			err,
			YANET_ERROR_RESOURCE_EXHAUSTED,
			"failed to lock the agent lease file: %s",
			strerror(saved)
		);
		return -1;
	}

	spinlock_lock(&lease_lock);
	if (lease_count == lease_capacity) {
		size_t new_capacity =
			(lease_capacity != 0) ? lease_capacity * 2 : 8;
		struct agent_lease_entry *new_leases =
			(struct agent_lease_entry *)realloc(
				leases, sizeof(*leases) * new_capacity
			);
		if (new_leases == NULL) {
			spinlock_unlock(&lease_lock);
			close(fd);
			yanet_error_add_kind(
				err,
				YANET_ERROR_RESOURCE_EXHAUSTED,
				"failed to record the agent lease"
			);
			return -1;
		}
		leases = new_leases;
		lease_capacity = new_capacity;
	}
	leases[lease_count].agent = agent;
	leases[lease_count].fd = fd;
	++lease_count;
	spinlock_unlock(&lease_lock);

	return 0;
}

void
agent_lease_release(struct agent *agent) {
	spinlock_lock(&lease_lock);
	for (size_t idx = 0; idx < lease_count; ++idx) {
		if (leases[idx].agent != agent) {
			continue;
		}
		close(leases[idx].fd);
		leases[idx] = leases[lease_count - 1];
		--lease_count;
		break;
	}
	spinlock_unlock(&lease_lock);
}

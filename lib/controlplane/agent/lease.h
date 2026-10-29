#pragma once

#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"

struct agent;
struct yanet_shm;

// Acquire the name lease for a freshly attaching agent.
//
// The lease is an exclusive lock on a zero-size file beside the
// segment's storage, held for the owner's process lifetime: while a
// live process owns the name, a same-name attach is refused here with
// failed precondition instead of superseding the owner's agent and
// reclaiming memory its calls may still touch. The kernel releases the
// lock when the holder exits by any means, so the answer is truthful
// across the process and IPC namespaces of a containerized deployment.
//
// A segment with no backing path (an embedded harness segment) has no
// lease: the call succeeds with nothing held.
//
// Returns 0 with the lease recorded for the agent, -1 with the error
// set when the name is held by a live owner or the lease file cannot
// be managed.
int
agent_lease_acquire(
	struct agent *agent,
	const struct yanet_shm *shm,
	uint32_t instance_idx,
	const char *agent_name,
	yanet_error **err
);

// Drop the name lease of an agent: a clean handoff that lets the next
// attach take the name without waiting for this process to exit.
//
// Safe on an agent that holds no lease.
void
agent_lease_release(struct agent *agent);

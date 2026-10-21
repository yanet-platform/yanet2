#pragma once

#include <stdbool.h>

struct agent;
struct cp_config;

// Report whether agent is still the registry head of its name.
//
// The caller must hold the agent lock, so the registry structure cannot
// change under the scan, and the configuration lock besides it, because
// every caller is an update path refusing items owned by a superseded
// agent: creating through a superseded agent would raise a
// predecessor's loaded count from zero and race the sweep's
// reclamation.
bool
agent_is_current(struct cp_config *cp_config, struct agent *agent);

// cp_config_reclaim_unused_agents_locked frees superseded agents that no
// live generation references.
//
// The caller must hold the agent lock so predecessor chains and their
// ownership counts remain stable for the complete sweep. The
// configuration lock is not needed: the sweep only ever reclaims
// predecessors, and a predecessor's loaded count cannot rise past an
// in-flight zero check — the only zero-to-one gains on a predecessor
// are a destroy gate's destruction hold and they take the agent lock,
// updates through a superseded agent are refused outright, and a copy
// gain mirrors a reference the source generation already holds. Reclamation
// retains the single-live-holder precondition for each agent name; issue #2001
// tracks enforcing that precondition.
void
cp_config_reclaim_unused_agents_locked(struct cp_config *cp_config);

// cp_config_reclaim_unused_agents is the unlocked form of the sweep
// above: it takes the agent lock, sweeps and releases it, waiting out
// a busy holder rather than leaving the reclamation to a later one.
//
// For paths that drop the last live reference to an agent outside the
// configuration lock, so a superseded agent cannot outlive the call
// that released it. A thread already holding the configuration lock
// skips the sweep and leaves the reclamation to its own exit path,
// which sweeps before releasing the lock.
void
cp_config_reclaim_unused_agents(struct cp_config *cp_config);

#pragma once

#include <stdbool.h>

struct agent;
struct cp_config;

// Report whether agent is still the registry head of its name. The
// caller must hold the configuration lock; see the implementation for
// the full contract.
bool
agent_is_current(struct cp_config *cp_config, struct agent *agent);

// cp_config_reclaim_unused_agents_locked frees superseded agents that no
// live generation references.
//
// The caller must hold the agent lock so predecessor chains and their
// ownership counts remain stable for the complete sweep. The configuration
// lock is not needed: the sweep only ever reclaims predecessors, and a
// predecessor's loaded count is monotone non-increasing — reference gains
// from zero only ever target a registry head, because updates through a
// superseded agent are refused, and a copy gain mirrors a reference the
// source generation already holds. Reclamation retains the
// single-live-holder precondition for each agent name; issue #2001 tracks
// enforcing that precondition.
void
cp_config_reclaim_unused_agents_locked(struct cp_config *cp_config);

#pragma once

struct cp_config;

// cp_config_reclaim_unused_agents_locked frees superseded agents that no
// live generation references.
//
// The caller must hold the configuration lock so predecessor chains and
// their ownership counts remain stable for the complete sweep. Reclamation
// retains the single-live-holder precondition for each agent name; issue
// #2001 tracks enforcing that precondition.
void
cp_config_reclaim_unused_agents_locked(struct cp_config *cp_config);

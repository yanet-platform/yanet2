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

// cp_config_reclaim_unused_agents is the unlocked form of the sweep above:
// it runs the sweep when the configuration lock is free, taking and
// releasing the lock around it, and defers otherwise.
//
// For paths that drop the last live reference to an agent outside the
// configuration lock, so a superseded agent cannot outlive the call that
// released it. Deferring is safe because every holder of the configuration
// lock sweeps before releasing it — the publish path, the attach, extension
// and listing paths, and the destroy gates.
void
cp_config_reclaim_unused_agents(struct cp_config *cp_config);

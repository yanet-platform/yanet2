#pragma once

struct cp_config;
struct dp_config;
struct agent;

// Allocate and initialise a system agent inside cp_config's memory zone.
//
// The agent's memory_context is parented to cp_config's so that any
// per-agent allocations land inside the controlplane zone.
//
// Returns the agent on success or NULL on out-of-memory.
struct agent *
dp_system_agent_new(
	struct cp_config *cp_config,
	struct dp_config *dp_config,
	const char *name
);

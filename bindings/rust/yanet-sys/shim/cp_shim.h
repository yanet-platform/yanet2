// Control-plane helpers over the header-inline pin of a generation.
//
// A read of what the dataplane runs pins the generation the configuration of
// the agent publishes, as a C module's api does, finds what it reads there and
// releases the pin. The pin is header-inline C, so the shim wraps it with
// hidden visibility, as the other shims do; only an api crate calls it.
#pragma once

#include "shim_visibility.h"

struct agent;
struct cp_config_gen;

// Pins the generation the configuration of the agent currently publishes, as
// cp_config_gen_acquire does.
//
// Every pin is released once by yanet_sys_cp_gen_release, however long the
// read runs and whether an update publishes a newer generation meanwhile.
YANET_SYS_HIDDEN struct cp_config_gen *
yanet_sys_cp_gen_acquire(struct agent *agent);

// Releases a pin yanet_sys_cp_gen_acquire took over the same agent.
//
// The last release of a generation an update has superseded tears it down,
// with what it alone holds.
YANET_SYS_HIDDEN void
yanet_sys_cp_gen_release(struct agent *agent, struct cp_config_gen *config_gen);

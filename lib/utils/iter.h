#pragma once

#include "lib/dataplane/config/zone.h"

////////////////////////////////////////////////////////////////////////////////

typedef void (*dp_config_iterate_module_configs_callback)(void *userdata, const char *device, const char *pipeline, const char *function, const char *chain, const char *module_type, const char *config);

////////////////////////////////////////////////////////////////////////////////

/// Iterate over entities in the controlplane config. 
// If entity name is not null, iterate only entities with such name.
void
dp_config_iterate_module_configs(struct dp_config *dp_config, 
    const char *device, 
    const char *pipeline, 
    const char *function, 
    const char *chain,
    const char *module_type,
    const char *config,
    void *userdata,
    dp_config_iterate_module_configs_callback
);
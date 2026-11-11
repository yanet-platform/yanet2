// Names and declarations the loader test and its plugin share.
#pragma once

// Name of the object type the plugin constructs.
#define LOADER_PROBE_NAME "loader_probe"

// Prepared buffer size the plugin's constructor declares; nonzero, so a
// loaded type carrying it proves the plugin's constructor ran.
#define LOADER_PROBE_PLUGIN_PREPARED_SIZE 24

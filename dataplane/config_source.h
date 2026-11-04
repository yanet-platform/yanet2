#pragma once

#include <stdio.h>

// dataplane_config_open returns an owned stream after resolving includes.
FILE *
dataplane_config_open(const char *path);

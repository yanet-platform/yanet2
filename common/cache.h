#pragma once

#ifndef YANET_CACHE_LINE_SIZE
// Standalone common-code builds do not include the DPDK configuration.
#define YANET_CACHE_LINE_SIZE 64
#endif

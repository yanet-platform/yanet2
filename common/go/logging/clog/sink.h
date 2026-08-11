#pragma once

#include "lib/logging/log.h"

// clogSink is defined in clog.go and exported to C via cgo.
extern void
clogSink(enum log_id level, char *file, int line, char *msg, void *ctx);

void
clog_install_sink(void);

#include "sink.h"

// Cgo cannot declare const parameters on an exported Go function, so the
// exported callback's type differs from the log_sink_fn typedef.
// This wrapper restores the qualifiers before calling it — casting the
// function pointer instead would be undefined behavior.
static void
clog_sink_trampoline(
	enum log_id level,
	const char *file,
	int line,
	const char *msg,
	void *ctx
) {
	clogSink(level, (char *)file, line, (char *)msg, ctx);
}

void
clog_install_sink(void) {
	log_set_sink(clog_sink_trampoline, NULL);
}

#include "sigbus.h"

#include <signal.h>
#include <unistd.h>

// A faulting shared-memory client must exit without touching the arena again.
//
// Only async-signal-safe operations are allowed here, including when the fault
// interrupts logging or allocation. Failure to write the diagnostic still
// exits.
static void
exit_on_sigbus(int signum) {
	static const char message[] =
		"SIGBUS: terminating YANET controlplane\n";
	ssize_t written = write(STDERR_FILENO, message, sizeof(message) - 1);
	(void)written;
	_exit(128 + signum);
}

int
yanet_install_sigbus_handler(void) {
	struct sigaction action = {0};
	action.sa_handler = exit_on_sigbus;
	// Go-managed threads require signal handlers to use their alternate
	// stack.
	action.sa_flags = SA_ONSTACK;
	sigfillset(&action.sa_mask);
	return sigaction(SIGBUS, &action, NULL);
}

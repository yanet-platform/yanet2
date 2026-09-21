#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include "sigbus.h"

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <sys/socket.h>
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
	// Pin the destination while other threads may still change descriptors.
	int descriptor =
		fcntl(STDERR_FILENO, F_DUPFD_CLOEXEC, STDERR_FILENO + 1);
	// Only pipes and sockets offer nonblocking output. Other destinations
	// are skipped because nonblocking flags cannot prevent file I/O waits.
	ssize_t written =
		send(descriptor,
		     message,
		     sizeof(message) - 1,
		     MSG_DONTWAIT | MSG_NOSIGNAL);
	if (written < 0 && errno == ENOTSOCK &&
	    fcntl(descriptor, F_GETPIPE_SZ) != -1) {
		int flags = fcntl(descriptor, F_GETFL);
		if (flags != -1 &&
		    fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) != -1) {
			written =
				write(descriptor, message, sizeof(message) - 1);
			(void)written;
		}
	}
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

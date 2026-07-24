#pragma once

// Portable CPU pause hint for busy-wait spin loops.
//
// Emits the architecture's spin-wait hint instruction where one exists, and
// falls back to a plain compiler barrier everywhere else. Every branch
// carries a "memory" clobber so the compiler cannot hoist a spin loop's
// loads out across the call, which matters on the fallback path just as
// much as on the instruction paths.
//
// This header must stay free of DPDK headers: it sits on the cgo boundary
// through common/rwlock.h and lib/fwstate/fwmap.h.
static inline void
cpu_pause(void) {
#if defined(__x86_64__) || defined(__i386__)
	__asm__ __volatile__("pause" ::: "memory");
#elif defined(__aarch64__)
	__asm__ __volatile__("yield" ::: "memory");
#else
	__asm__ __volatile__("" ::: "memory");
#endif
}

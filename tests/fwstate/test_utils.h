/**
 * @file test_colors.h
 * @brief TTY-aware color support for test output
 *
 * This header provides ANSI color codes that are automatically enabled
 * when output goes to a TTY (terminal) and disabled when output is
 * redirected to a pipe or file.
 */

#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#include "common/memory.h"
#include "common/memory_block.h"

/**
 * @brief Check if colors should be enabled based on TTY detection
 * @return 1 if colors should be enabled, 0 otherwise
 */
static inline int
should_use_colors(void) {
	static int colors_enabled = -1;

	if (colors_enabled == -1) {
		// Check if stdout is connected to a terminal
		colors_enabled = isatty(STDOUT_FILENO) ? 1 : 0;
	}

	return colors_enabled;
}

/**
 * @brief Static color arrays - with and without ANSI codes
 */
static const char *const color_codes_enabled[] = {
	"\033[0m",  // RESET
	"\033[31m", // RED
	"\033[32m", // GREEN
	"\033[33m", // YELLOW
	"\033[34m", // BLUE
	"\033[35m", // MAGENTA
	"\033[36m", // CYAN
	"\033[37m", // WHITE
	"\033[1m",  // BOLD
	"\033[90m", // GRAY
	"\033[91m", // BRIGHT_RED
	"\033[92m", // BRIGHT_GREEN
	"\033[93m", // BRIGHT_YELLOW
	"\033[94m", // BRIGHT_BLUE
	"\033[95m", // BRIGHT_MAGENTA
	"\033[96m", // BRIGHT_CYAN
	"\033[97m"  // BRIGHT_WHITE
};

static const char *const color_codes_disabled[] = {
	"", // RESET
	"", // RED
	"", // GREEN
	"", // YELLOW
	"", // BLUE
	"", // MAGENTA
	"", // CYAN
	"", // WHITE
	"", // BOLD
	"", // GRAY
	"", // BRIGHT_RED
	"", // BRIGHT_GREEN
	"", // BRIGHT_YELLOW
	"", // BRIGHT_BLUE
	"", // BRIGHT_MAGENTA
	"", // BRIGHT_CYAN
	""  // BRIGHT_WHITE
};

/**
 * @brief Color indices for the arrays above
 */
// NOLINTBEGIN(readability-identifier-naming)
typedef enum {
	COLOR_RESET = 0,
	COLOR_RED,
	COLOR_GREEN,
	COLOR_YELLOW,
	COLOR_BLUE,
	COLOR_MAGENTA,
	COLOR_CYAN,
	COLOR_WHITE,
	COLOR_BOLD,
	COLOR_GRAY,
	COLOR_BRIGHT_RED,
	COLOR_BRIGHT_GREEN,
	COLOR_BRIGHT_YELLOW,
	COLOR_BRIGHT_BLUE,
	COLOR_BRIGHT_MAGENTA,
	COLOR_BRIGHT_CYAN,
	COLOR_BRIGHT_WHITE,
	COLOR_COUNT
} color_index_t;
// NOLINTEND(readability-identifier-naming)

/**
 * @brief Get the appropriate color string based on TTY detection
 * @param color_idx Index of the color to retrieve
 * @return Color string (either ANSI code or empty string)
 */
static inline const char *
get_color(color_index_t color_idx) {
	if (color_idx >= COLOR_COUNT) {
		return "";
	}

	return should_use_colors() ? color_codes_enabled[color_idx]
				   : color_codes_disabled[color_idx];
}

/**
 * @brief Convenience macros for common colors
 * These automatically adapt to TTY vs pipe output
 */
#define C_RESET get_color(COLOR_RESET)
#define C_RED get_color(COLOR_RED)
#define C_GREEN get_color(COLOR_GREEN)
#define C_YELLOW get_color(COLOR_YELLOW)
#define C_BLUE get_color(COLOR_BLUE)
#define C_MAGENTA get_color(COLOR_MAGENTA)
#define C_CYAN get_color(COLOR_CYAN)
#define C_WHITE get_color(COLOR_WHITE)
#define C_BOLD get_color(COLOR_BOLD)
#define C_GRAY get_color(COLOR_GRAY)

/**
 * @brief Convenience macros for bright colors
 */
#define C_BRIGHT_RED get_color(COLOR_BRIGHT_RED)
#define C_BRIGHT_GREEN get_color(COLOR_BRIGHT_GREEN)
#define C_BRIGHT_YELLOW get_color(COLOR_BRIGHT_YELLOW)
#define C_BRIGHT_BLUE get_color(COLOR_BRIGHT_BLUE)
#define C_BRIGHT_MAGENTA get_color(COLOR_BRIGHT_MAGENTA)
#define C_BRIGHT_CYAN get_color(COLOR_BRIGHT_CYAN)
#define C_BRIGHT_WHITE get_color(COLOR_BRIGHT_WHITE)

/**
 * @brief Convenience macros for common color combinations
 */
#define C_SUCCESS C_BOLD C_GREEN
#define C_ERROR C_BOLD C_RED
#define C_WARNING C_BOLD C_YELLOW
#define C_INFO C_BOLD C_BLUE
#define C_HEADER C_BOLD C_WHITE

/**
 * @brief Force enable or disable colors (for testing purposes)
 * @param enable 1 to force enable, 0 to force disable, -1 to use auto-detection
 */
static inline void
force_colors(int enable) {
	static int forced_state = -1;
	forced_state = enable;

	// Override the should_use_colors function behavior
	if (forced_state != -1) {
		// This is a bit of a hack, but we'll modify the static variable
		// by calling should_use_colors and then overriding its result
		should_use_colors();
	}
}

static inline void *
allocate_locked_memory(size_t size) {
	void *storage =
		mmap(NULL,
		     size,
		     PROT_READ | PROT_WRITE,
		     MAP_PRIVATE | MAP_ANON,
		     -1,
		     0);

	if (storage == MAP_FAILED) {
		perror("Failed to mmap hugepages storage");
		return NULL;
	}

	return storage;
}

static inline void
free_arena(void *ptr, size_t size) {
	if (ptr) {
		munmap(ptr, size);
	}
}

static inline struct memory_context *
init_context_from_arena(void *arena, size_t arena_size, const char *name) {
	struct memory_context *ctx = (struct memory_context *)arena;
	memset(ctx, 0, sizeof(struct memory_context));

	struct block_allocator *ba = (struct block_allocator *)(ctx + 1);
	memset(ba, 0, sizeof(struct block_allocator));
	block_allocator_init(ba);

	void *arena_data = (uint8_t *)(ba + 1);
	block_allocator_put_arena(
		ba,
		arena_data,
		arena_size - sizeof(struct memory_context) -
			sizeof(struct block_allocator)
	);
	memory_context_init(ctx, name, ba);
	return ctx;
}

static inline void
verify_memory_leaks(const struct memory_context *ctx, const char *test_name) {
	if (ctx->balloc_count != ctx->bfree_count) {
		fprintf(stderr,
			"[%s] Memory leak detected by count: allocs=%zu, "
			"frees=%zu\n",
			test_name,
			ctx->balloc_count,
			ctx->bfree_count);
		assert(false);
	}

	if (ctx->balloc_size != ctx->bfree_size) {
		fprintf(stderr,
			"[%s] Memory leak detected by size: allocated=%zu, "
			"freed=%zu\n",
			test_name,
			ctx->balloc_size,
			ctx->bfree_size);
		assert(false);
	}
}

static inline double
get_time(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec + ts.tv_nsec / 1000000000.0;
}

/**
 * @brief Format a number in human-readable form with appropriate units
 * @param num The number to format
 * @return Pointer to the formatted string
 *
 * NOTE: This function is not thread-safe
 */
static inline char *
numfmt(size_t num) {
#define BUF_SIZE 1024
	static int offset = 0;
	static char buf_data[BUF_SIZE];
	const char *units[] = {"", "K", "M", "G", "T"};

	int unit_index = 0;
	double value = (double)num;

	while (value >= 1000.0 && unit_index < 4) {
		value /= 1000.0;
		unit_index++;
	}

	char *buf = &buf_data[offset];
	if (unit_index == 0) {
		snprintf(buf, BUF_SIZE, "%zu", num);
	} else if (value == (int)value) {
		snprintf(buf, BUF_SIZE, "%d%s", (int)value, units[unit_index]);
	} else {
		snprintf(buf, BUF_SIZE, "%.1f%s", value, units[unit_index]);
	}

	buf[offset + BUF_SIZE - 1] = '\0';
	offset = (offset + BUF_SIZE) % sizeof(buf_data);
	return buf;
}

#include <stdio.h>

typedef void (*func_cb)(int *data, int y);
typedef void (*free_cb)(int *data);

inline void
my_cb(int *data, int y) {
	*data += y;
}

inline void
my_free_cb(int *data) {
	*data = 0;
}

inline void
my_cb1(int *data, int y) {
	*data += y * 2;
}

inline void
my_free_cb1(int *data) {
	*data = 0;
}

struct attr {
	func_cb func;
	free_cb free;
};

static const struct attr a1 = {my_cb, my_free_cb};
static const struct attr a2 = {my_cb1, my_free_cb1};

// static const struct attr *attrs[] = {&a1, &a2};

#define INIT(...) static const struct attr *__attrs[] = {__VA_ARGS__};

#define QUERY(x, y)                                                            \
	do {                                                                   \
		typeof(y) _y = (y);                                            \
		for (size_t i = 0;                                             \
		     i < sizeof(__attrs) / sizeof(struct attr *);              \
		     ++i) {                                                    \
			(__attrs[i])->func(x, _y);                             \
		}                                                              \
	} while (0)

#define FREE(x)                                                                \
	do {                                                                   \
		for (size_t i = 0;                                             \
		     i < sizeof(__attrs) / sizeof(struct attr *);              \
		     ++i) {                                                    \
			(__attrs[i])->free(x);                                 \
		}                                                              \
	} while (0)

int
main() {
	int x;
	scanf("%d", &x);
	int y;
	scanf("%d", &y);
	INIT(&a1, &a2);
	QUERY(&x, y);
	printf("%d\n", x);
	FREE(&x);
	return 0;
}
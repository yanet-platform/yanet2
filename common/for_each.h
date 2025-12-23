#pragma once

#define FE_1(M, x) M(x)
#define FE_2(M, x, ...) M(x), FE_1(M, __VA_ARGS__)
#define FE_3(M, x, ...) M(x), FE_2(M, __VA_ARGS__)
#define FE_4(M, x, ...) M(x), FE_3(M, __VA_ARGS__)
#define FE_5(M, x, ...) M(x), FE_4(M, __VA_ARGS__)
#define FE_6(M, x, ...) M(x), FE_5(M, __VA_ARGS__)
#define FE_7(M, x, ...) M(x), FE_6(M, __VA_ARGS__)
#define FE_8(M, x, ...) M(x), FE_7(M, __VA_ARGS__)
#define FE_9(M, x, ...) M(x), FE_8(M, __VA_ARGS__)
#define FE_10(M, x, ...) M(x), FE_9(M, __VA_ARGS__)

#define GET_FE(_1, _2, _3, _4, _5, _6, _7, _8, _9, _10, NAME, ...) NAME
#define FOR_EACH(M, ...)                                                       \
	GET_FE(__VA_ARGS__,                                                    \
	       FE_10,                                                          \
	       FE_9,                                                           \
	       FE_8,                                                           \
	       FE_7,                                                           \
	       FE_6,                                                           \
	       FE_5,                                                           \
	       FE_4,                                                           \
	       FE_3,                                                           \
	       FE_2,                                                           \
	       FE_1)(M, __VA_ARGS__)

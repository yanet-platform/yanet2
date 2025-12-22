#pragma once

#ifndef unlikely
#define unlikely(x) __builtin_expect(!!(x), 0)
#endif // unlikely

#ifndef likely
#define likely(x) __builtin_expect(!!(x), 1)
#endif // likely
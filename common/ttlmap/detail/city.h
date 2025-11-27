// Copyright (c) 2011 Google, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
//
// CityHash, by Geoff Pike and Jyrki Alakuijala
//
// This file provides CityHash64() and related functions.
//
// It's probably possible to create even faster hash functions by
// writing a program that systematically explores some of the space of
// possible hash functions, by using SIMD instructions, or by
// compromising on hash quality.

#pragma once

#include <stdint.h>
#include <stdlib.h> // for size_t.

typedef uint8_t uint8;
typedef uint32_t uint32;
typedef uint64_t uint64;

/* Define to 1 if the compiler supports __builtin_expect. */
#define HAVE_BUILTIN_EXPECT 1

/* Define to 1 if you have the <dlfcn.h> header file. */
#define HAVE_DLFCN_H 1

/* Define to 1 if you have the <inttypes.h> header file. */
#define HAVE_INTTYPES_H 1

/* Define to 1 if you have the <memory.h> header file. */
#define HAVE_MEMORY_H 1

/* Define to 1 if you have the <stdint.h> header file. */
#define HAVE_STDINT_H 1

/* Define to 1 if you have the <stdlib.h> header file. */
#define HAVE_STDLIB_H 1

/* Define to 1 if you have the <strings.h> header file. */
#define HAVE_STRINGS_H 1

/* Define to 1 if you have the <string.h> header file. */
#define HAVE_STRING_H 1

/* Define to 1 if you have the <sys/stat.h> header file. */
#define HAVE_SYS_STAT_H 1

/* Define to 1 if you have the <sys/types.h> header file. */
#define HAVE_SYS_TYPES_H 1

/* Define to 1 if you have the <unistd.h> header file. */
#define HAVE_UNISTD_H 1

/* Define to the sub-directory in which libtool stores uninstalled libraries.
 */
#define LT_OBJDIR ".libs/"

/* Define to the address where bug reports for this package should be sent. */
#define PACKAGE_BUGREPORT "cityhash-discuss@googlegroups.com"

/* Define to the full name of this package. */
#define PACKAGE_NAME "CityHash"

/* Define to the full name and version of this package. */
#define PACKAGE_STRING "CityHash 1.1.1"

/* Define to the one symbol short name of this package. */
#define PACKAGE_TARNAME "cityhash"

/* Define to the home page for this package. */
#define PACKAGE_URL ""

/* Define to the version of this package. */
#define PACKAGE_VERSION "1.1.1"

/* Define to 1 if you have the ANSI C header files. */
#define STDC_HEADERS 1

/* Define WORDS_BIGENDIAN to 1 if your processor stores words with the most
   significant byte first (like Motorola and SPARC, unlike Intel). */
#if defined AC_APPLE_UNIVERSAL_BUILD
#if defined __BIG_ENDIAN__
#define WORDS_BIGENDIAN 1
#endif
#else
#ifndef WORDS_BIGENDIAN
/* #  undef WORDS_BIGENDIAN */
#endif
#endif

/* Define for Solaris 2.5.1 so the uint32_t typedef from <sys/synch.h>,
   <pthread.h>, or <semaphore.h> is not used. If the typedef were allowed, the
   #define below would cause a syntax error. */
/* #undef _UINT32_T */

/* Define for Solaris 2.5.1 so the uint64_t typedef from <sys/synch.h>,
   <pthread.h>, or <semaphore.h> is not used. If the typedef were allowed, the
   #define below would cause a syntax error. */
/* #undef _UINT64_T */

/* Define for Solaris 2.5.1 so the uint8_t typedef from <sys/synch.h>,
   <pthread.h>, or <semaphore.h> is not used. If the typedef were allowed, the
   #define below would cause a syntax error. */
/* #undef _UINT8_T */

/* Define to `__inline__' or `__inline' if that's what the C compiler
   calls it, or to nothing if 'inline' is not supported under any name.  */
#ifndef __cplusplus
/* #undef inline */
#endif

#include <string.h> // for memcpy and memset

static inline uint64
unaligned_load64(const char *p) {
	uint64 result;
	memcpy(&result, p, sizeof(result));
	return result;
}

static inline uint32
unaligned_load32(const char *p) {
	uint32 result;
	memcpy(&result, p, sizeof(result));
	return result;
}

#ifdef _MSC_VER

#include <stdlib.h>
#define bswap_32(x) _byteswap_ulong(x)
#define bswap_64(x) _byteswap_uint64(x)

#elif defined(__APPLE__)

// Mac OS X / Darwin features
#include <libkern/OSByteOrder.h>
#define bswap_32(x) OSSwapInt32(x)
#define bswap_64(x) OSSwapInt64(x)

#elif defined(__sun) || defined(sun)

#include <sys/byteorder.h>
#define bswap_32(x) BSWAP_32(x)
#define bswap_64(x) BSWAP_64(x)

#elif defined(__FreeBSD__)

#include <sys/endian.h>
#define bswap_32(x) bswap32(x)
#define bswap_64(x) bswap64(x)

#elif defined(__OpenBSD__)

#include <sys/types.h>
#define bswap_32(x) swap32(x)
#define bswap_64(x) swap64(x)

#elif defined(__NetBSD__)

#include <machine/bswap.h>
#include <sys/types.h>
#if defined(__BSWAP_RENAME) && !defined(__bswap_32)
#define bswap_32(x) bswap32(x)
#define bswap_64(x) bswap64(x)
#endif

#else

#include <byteswap.h>

#endif

#ifdef WORDS_BIGENDIAN
#define uint32_in_expected_order(x) (bswap_32(x))
#define uint64_in_expected_order(x) (bswap_64(x))
#else
#define uint32_in_expected_order(x) (x)
#define uint64_in_expected_order(x) (x)
#endif

#if !defined(LIKELY)
#if HAVE_BUILTIN_EXPECT
#define LIKELY(x) (__builtin_expect(!!(x), 1))
#else
#define LIKELY(x) (x)
#endif
#endif

static inline uint32
fetch32(const char *p) {
	return uint32_in_expected_order(unaligned_load32(p));
}

// Some primes between 2^63 and 2^64 for various uses.
static const uint64 k0 = 0xc3a5c85c97cb3127ULL;
static const uint64 k1 = 0xb492b66fbe98f273ULL;
static const uint64 k2 = 0x9ae16a3b2f90404fULL;

// Magic numbers for 32-bit hashing.  Copied from Murmur3.
static const uint32 c1 = 0xcc9e2d51;
static const uint32 c2 = 0x1b873593;

// A 32-bit to 32-bit integer hash copied from Murmur3.
static inline uint32
fmix(uint32 h) {
	h ^= h >> 16;
	h *= 0x85ebca6b;
	h ^= h >> 13;
	h *= 0xc2b2ae35;
	h ^= h >> 16;
	return h;
}

static inline uint32
rotate32(uint32 val, int shift) {
	// Avoid shifting by 32: doing so yields an undefined result.
	return shift == 0 ? val : ((val >> shift) | (val << (32 - shift)));
}

#undef PERMUTE3_32
#define PERMUTE3_32(a, b, c)                                                   \
	do {                                                                   \
		uint32_t t = a;                                                \
		a = c;                                                         \
		c = b;                                                         \
		b = t;                                                         \
	} while (0)

#undef PERMUTE3_64
#define PERMUTE3_64(a, b, c)                                                   \
	do {                                                                   \
		uint64 t = a;                                                  \
		a = c;                                                         \
		c = b;                                                         \
		b = t;                                                         \
	} while (0)

static inline uint32
mur(uint32 a, uint32 h) {
	// Helper from Murmur3 for combining two 32-bit values.
	a *= c1;
	a = rotate32(a, 17);
	a *= c2;
	h ^= a;
	h = rotate32(h, 19);
	return h * 5 + 0xe6546b64;
}

static inline uint32
hash32_len13to24(const char *s, size_t len) {
	uint32 a = fetch32(s - 4 + (len >> 1));
	uint32 b = fetch32(s + 4);
	uint32 c = fetch32(s + len - 8);
	uint32 d = fetch32(s + (len >> 1));
	uint32 e = fetch32(s);
	uint32 f = fetch32(s + len - 4);
	uint32 h = (uint32)(len);

	return fmix(mur(f, mur(e, mur(d, mur(c, mur(b, mur(a, h)))))));
}

static inline uint32
hash32_len0to4(const char *s, size_t len) {
	uint32 b = 0;
	uint32 c = 9;
	for (size_t i = 0; i < len; i++) {
		signed char v = (signed char)(s[i]);
		b = b * c1 + (uint32)(v);
		c ^= b;
	}
	return fmix(mur(b, mur((uint32)(len), c)));
}

static inline uint32
hash32_len5to12(const char *s, size_t len) {
	uint32 a = (uint32)(len), b = a * 5, c = 9, d = b;
	a += fetch32(s);
	b += fetch32(s + len - 4);
	c += fetch32(s + ((len >> 1) & 4));
	return fmix(mur(c, mur(b, mur(a, d))));
}

static inline uint32
city_hash32(const char *s, size_t len) {
	if (len <= 24) {
		return len <= 12 ? (len <= 4 ? hash32_len0to4(s, len)
					     : hash32_len5to12(s, len))
				 : hash32_len13to24(s, len);
	}

	// len > 24
	uint32 h = (uint32)(len), g = c1 * h, f = g;
	uint32 a0 = rotate32(fetch32(s + len - 4) * c1, 17) * c2;
	uint32 a1 = rotate32(fetch32(s + len - 8) * c1, 17) * c2;
	uint32 a2 = rotate32(fetch32(s + len - 16) * c1, 17) * c2;
	uint32 a3 = rotate32(fetch32(s + len - 12) * c1, 17) * c2;
	uint32 a4 = rotate32(fetch32(s + len - 20) * c1, 17) * c2;
	h ^= a0;
	h = rotate32(h, 19);
	h = h * 5 + 0xe6546b64;
	h ^= a2;
	h = rotate32(h, 19);
	h = h * 5 + 0xe6546b64;
	g ^= a1;
	g = rotate32(g, 19);
	g = g * 5 + 0xe6546b64;
	g ^= a3;
	g = rotate32(g, 19);
	g = g * 5 + 0xe6546b64;
	f += a4;
	f = rotate32(f, 19);
	f = f * 5 + 0xe6546b64;
	size_t iters = (len - 1) / 20;
	do {
		uint32 a0 = rotate32(fetch32(s) * c1, 17) * c2;
		uint32 a1 = fetch32(s + 4);
		uint32 a2 = rotate32(fetch32(s + 8) * c1, 17) * c2;
		uint32 a3 = rotate32(fetch32(s + 12) * c1, 17) * c2;
		uint32 a4 = fetch32(s + 16);
		h ^= a0;
		h = rotate32(h, 18);
		h = h * 5 + 0xe6546b64;
		f += a1;
		f = rotate32(f, 19);
		f = f * c1;
		g += a2;
		g = rotate32(g, 18);
		g = g * 5 + 0xe6546b64;
		h ^= a3 + a1;
		h = rotate32(h, 19);
		h = h * 5 + 0xe6546b64;
		g ^= a4;
		g = bswap_32(g) * 5;
		h += a4 * 5;
		h = bswap_32(h);
		f += a0;
		PERMUTE3_32(f, h, g);
		s += 20;
	} while (--iters != 0);
	g = rotate32(g, 11) * c1;
	g = rotate32(g, 17) * c1;
	f = rotate32(f, 11) * c1;
	f = rotate32(f, 17) * c1;
	h = rotate32(h + g, 19);
	h = h * 5 + 0xe6546b64;
	h = rotate32(h, 17) * c1;
	h = rotate32(h + f, 19);
	h = h * 5 + 0xe6546b64;
	h = rotate32(h, 17) * c1;
	return h;
}
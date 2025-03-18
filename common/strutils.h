#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <string.h>
#include <sys/types.h>

#include <errno.h>

ssize_t
strtcpy(char *restrict dst, const char *restrict src, size_t dsize) {
	bool trunc;
	size_t dlen, slen;

	if (dsize == 0) {
		errno = ENOBUFS;
		return -1;
	}

	slen = strnlen(src, dsize);
	trunc = (slen == dsize);
	dlen = slen - trunc;

	stpcpy(mempcpy(dst, src, dlen), "");
	if (trunc)
		errno = E2BIG;
	return trunc ? -1 : (ssize_t)slen;
}

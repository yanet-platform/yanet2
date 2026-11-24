//go:build linux && (amd64 || arm64)

package registry

import "strings"

// keySeed seeds the hash of registry keys (REGISTRY_HASH_SEED).
const keySeed = 0x9e3779b9

// FNV1a hashes a key as str_index_fnv1a does.
//
// A tail of fewer than four bytes is hashed last byte first, as C hashes
// it: the slot a key lands in is part of the shared format.
func FNV1a[Key string | []byte](key Key, seed uint32) uint64 {
	const prime = 1099511628211
	hash := uint64(14695981039346656037) ^ uint64(seed)
	whole := len(key) &^ 3
	for idx := range whole {
		hash = (hash ^ uint64(key[idx])) * prime
	}
	for idx := len(key) - 1; idx >= whole; idx-- {
		hash = (hash ^ uint64(key[idx])) * prime
	}
	return hash
}

// HashName hashes a one-field registry key, as registry_hash_name does.
func HashName[Key string | []byte](name Key) uint64 {
	return FNV1a(name, keySeed)
}

// HashNamePair hashes a two-field registry key: the first field's hash
// seeds the second's, as registry_hash_name_pair does.
func HashNamePair[Key string | []byte](first, second Key) uint64 {
	return FNV1a(second, uint32(FNV1a(first, keySeed)))
}

// fold folds a key hash into the 32 bits a registry's index probes with.
func fold(hash uint64) uint32 {
	return uint32(hash ^ hash>>32)
}

// FieldKey returns what of a string a C character field of that size
// keeps: the bytes before any NUL, at most one short of the field.
//
// C bounds a key by its field, reading up to the terminator or copying it
// in one byte short of the field. Every key here comes from a terminated
// field or from a name refused when it does not fit, so both readings
// agree and one cut serves every registry.
func FieldKey(value string, size int) string {
	if end := strings.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	return value[:min(len(value), size-1)]
}

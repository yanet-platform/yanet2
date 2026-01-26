package dataplane

import "unsafe"

type Alloc struct {
	alloc     unsafe.Pointer
	allocFunc unsafe.Pointer
}

func NewAlloc(alloc unsafe.Pointer, allocFunc unsafe.Pointer) *Alloc {
	return &Alloc{
		alloc:     alloc,
		allocFunc: allocFunc,
	}
}

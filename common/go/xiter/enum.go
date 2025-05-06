package xiter

import (
	"iter"
)

func Enumerate[T any](seq iter.Seq[T]) iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		idx := 0
		for v := range seq {
			if !yield(idx, v) {
				return
			}

			idx++
		}
	}
}

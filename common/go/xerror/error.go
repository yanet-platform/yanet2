package xerror

func Unwrap[T any](t T, e error) T {
	if e != nil {
		panic(e)
	}
	return t
}

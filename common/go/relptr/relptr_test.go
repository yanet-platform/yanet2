package relptr

import (
	"testing"
	"unsafe"
)

type testStruct struct {
	Value uint64
}

func TestDerefAndSet(t *testing.T) {
	var field *testStruct
	target := testStruct{Value: 42}

	Set(&field, &target)

	got := Deref(&field)
	if got == nil {
		t.Fatal("Deref returned nil")
	}
	if got != &target {
		t.Fatalf("Deref returned %p, want %p", got, &target)
	}
	if got.Value != 42 {
		t.Fatalf("Value = %d, want 42", got.Value)
	}
}

func TestDerefNil(t *testing.T) {
	var field *testStruct // zero bytes = null relative pointer

	got := Deref(&field)
	if got != nil {
		t.Fatalf("Deref of zero field returned %p, want nil", got)
	}
}

func TestSetNil(t *testing.T) {
	var field *testStruct
	target := testStruct{Value: 1}

	Set(&field, &target)
	raw := *(*uintptr)(unsafe.Pointer(&field))
	if raw == 0 {
		t.Fatal("field should be non-zero after Set")
	}

	Set(&field, nil)
	raw = *(*uintptr)(unsafe.Pointer(&field))
	if raw != 0 {
		t.Fatalf("field = %d, want 0 after Set(nil)", raw)
	}
}

func TestSlice(t *testing.T) {
	items := [4]testStruct{
		{Value: 10},
		{Value: 20},
		{Value: 30},
		{Value: 40},
	}

	var field *testStruct
	Set(&field, &items[0])

	got := Slice(&field, 4)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	for i, v := range got {
		want := uint64((i + 1) * 10)
		if v.Value != want {
			t.Errorf("got[%d].Value = %d, want %d", i, v.Value, want)
		}
	}

	// Verify zero-copy: mutate through slice, read from original.
	got[2].Value = 99
	if items[2].Value != 99 {
		t.Fatal("slice is not zero-copy")
	}
}

func TestSliceZeroCount(t *testing.T) {
	var field *testStruct
	target := testStruct{Value: 1}
	Set(&field, &target)

	got := Slice(&field, 0)
	if got != nil {
		t.Fatalf("Slice with count=0 returned non-nil")
	}
}

func TestSliceNilField(t *testing.T) {
	var field *testStruct

	got := Slice(&field, 5)
	if got != nil {
		t.Fatalf("Slice of nil field returned non-nil")
	}
}

func TestDerefOpaque(t *testing.T) {
	var field uintptr
	target := testStruct{Value: 77}

	SetOpaque(&field, unsafe.Pointer(&target))

	got := DerefOpaque(&field)
	if got == nil {
		t.Fatal("DerefOpaque returned nil")
	}
	if (*testStruct)(got).Value != 77 {
		t.Fatal("wrong value")
	}
}

func TestDerefOpaqueNil(t *testing.T) {
	var field uintptr
	got := DerefOpaque(&field)
	if got != nil {
		t.Fatal("expected nil")
	}
}

// TestRelativePointerInStruct simulates a C-like struct with a relative
// pointer field, similar to how balancer_vs.reals works in shared memory.
func TestRelativePointerInStruct(t *testing.T) {
	type container struct {
		Items    *uint64
		Count    uint32
		_        uint32
		Metadata uint64
	}

	items := [3]uint64{100, 200, 300}

	var c container
	c.Count = 3
	c.Metadata = 0xDEAD

	Set(&c.Items, &items[0])

	// Resolve and iterate.
	slice := Slice(&c.Items, c.Count)
	if len(slice) != 3 {
		t.Fatalf("len = %d, want 3", len(slice))
	}
	for i, v := range slice {
		want := uint64((i + 1) * 100)
		if v != want {
			t.Errorf("slice[%d] = %d, want %d", i, v, want)
		}
	}

	// Ensure container fields are not corrupted.
	if c.Metadata != 0xDEAD {
		t.Fatalf("Metadata corrupted: %x", c.Metadata)
	}
}

// TestFieldEmbeddedInContiguousMemory allocates a flat byte buffer and
// uses relative pointers between regions, mimicking shared memory layout.
func TestFieldEmbeddedInContiguousMemory(t *testing.T) {
	type header struct {
		DataPtr *uint64
		Len     uint32
		_       uint32
	}

	// Simulate a contiguous shared memory region.
	buf := make([]byte, 256)

	hdr := (*header)(unsafe.Pointer(&buf[0]))
	hdr.Len = 4

	// Place data at offset 64 within the same buffer.
	dataStart := (*uint64)(unsafe.Pointer(&buf[64]))
	data := unsafe.Slice(dataStart, 4)
	data[0] = 1
	data[1] = 2
	data[2] = 3
	data[3] = 4

	Set(&hdr.DataPtr, dataStart)

	got := Slice(&hdr.DataPtr, hdr.Len)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	for i, v := range got {
		if v != uint64(i+1) {
			t.Errorf("got[%d] = %d, want %d", i, v, i+1)
		}
	}
}

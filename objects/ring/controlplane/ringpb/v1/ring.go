package ringpb

import (
	"fmt"
	"math"
	"strings"
)

// MaxRingNameLen is the size of the C object-name buffer.
//
// The size includes the terminating NUL. So the longest accepted name is
// one byte shorter.
const MaxRingNameLen = 80

// MinRingCapacity is the smallest per-worker capacity of a ring, in bytes.
//
// It holds exactly one record header with an empty payload.
const MinRingCapacity = 8

// DefaultPublishBatch is the publish batch used when the request leaves it
// unset.
//
// The publish batch is the number of records a writer commits before it
// publishes them by itself.
const DefaultPublishBatch = 8

// MaxPublishBatch is the largest publish batch a ring accepts.
const MaxPublishBatch = 1024

// ValidateRingName checks a ring name against the C object-name rules.
//
// On failure, the error names the given proto field.
func ValidateRingName(field, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.IndexByte(name, 0) != -1 {
		return fmt.Errorf("%s must not contain NUL", field)
	}
	if len(name) >= MaxRingNameLen {
		return fmt.Errorf("%s must be shorter than %d bytes", field, MaxRingNameLen)
	}
	return nil
}

// Validate checks the ring name, the per-worker capacity and the publish
// batch.
//
// An unset publish batch means the default. The C layer sets the upper
// limit of the capacity, and that limit depends on the build. The service
// reports a capacity the C layer rejects as InvalidArgument. A capacity
// that does not fit in 32 bits is rejected here. The C API takes 32 bits,
// so a cut value would quietly create a much smaller ring.
func (m *CreateRingRequest) Validate() error {
	if err := ValidateRingName("name", m.GetName()); err != nil {
		return err
	}

	capacity := m.GetCapacity()
	if capacity > math.MaxUint32 {
		return fmt.Errorf(
			"capacity %d exceeds the maximum representable value %d",
			capacity, uint32(math.MaxUint32),
		)
	}
	if capacity&(capacity-1) != 0 {
		return fmt.Errorf("capacity %d must be a power of two", capacity)
	}
	if capacity < MinRingCapacity {
		return fmt.Errorf(
			"capacity %d must be at least %d",
			capacity, MinRingCapacity,
		)
	}
	if batch := m.GetPublishBatch(); batch > MaxPublishBatch {
		return fmt.Errorf(
			"publish_batch %d must be at most %d records",
			batch, MaxPublishBatch,
		)
	}
	return nil
}

// PublishBatchOrDefault returns the requested publish batch, or
// DefaultPublishBatch when the request leaves it unset.
func (m *CreateRingRequest) PublishBatchOrDefault() uint32 {
	if batch := m.GetPublishBatch(); batch != 0 {
		return batch
	}
	return DefaultPublishBatch
}

// Validate checks that the request names the ring to describe.
func (m *ShowRingRequest) Validate() error {
	return ValidateRingName("name", m.GetName())
}

// Validate checks that the request names the ring to delete.
func (m *DeleteRingRequest) Validate() error {
	return ValidateRingName("name", m.GetName())
}

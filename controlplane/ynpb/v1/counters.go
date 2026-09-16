package ynpb

import (
	"errors"
	"fmt"
	"strings"
)

// MaxCounterTagKeyLen is the size of the fixed counter-tag key buffer,
// including its terminating NUL.
const MaxCounterTagKeyLen = 80

// MaxCounterTagValueLen is the size of the fixed counter-tag value buffer,
// including its terminating NUL.
const MaxCounterTagValueLen = 80

const maxQueryPatterns = 64

func (m *CountersByTagsRequest) Validate() error {
	if len(m.GetQuery()) > maxQueryPatterns {
		return fmt.Errorf(
			"query carries %d patterns, at most %d are accepted",
			len(m.GetQuery()),
			maxQueryPatterns,
		)
	}

	for idx, pattern := range m.GetQuery() {
		if strings.IndexByte(pattern, 0) != -1 {
			return fmt.Errorf("query[%d] must not contain NUL", idx)
		}
	}

	for idx, tag := range m.GetTags() {
		if err := tag.Validate(); err != nil {
			return fmt.Errorf("tags[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *CounterTag) Validate() error {
	if strings.IndexByte(m.GetKey(), 0) != -1 {
		return errors.New("key must not contain NUL")
	}
	if strings.IndexByte(m.GetValue(), 0) != -1 {
		return errors.New("value must not contain NUL")
	}
	if len(m.GetKey()) >= MaxCounterTagKeyLen {
		return fmt.Errorf("key must be shorter than %d bytes", MaxCounterTagKeyLen)
	}
	if len(m.GetValue()) >= MaxCounterTagValueLen {
		return fmt.Errorf(
			"value must be shorter than %d bytes",
			MaxCounterTagValueLen,
		)
	}

	return nil
}

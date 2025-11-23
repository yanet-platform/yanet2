package lib

import (
	"fmt"
)

// ConversionError represents an error that occurred during test conversion.
// It includes context about which test and step caused the error.
type ConversionError struct {
	TestName string // Name of the test being converted
	StepType string // Type of step that failed (e.g., "ipv4Update", "sendPackets")
	StepIndex int   // 1-based index of the step (0 if test-level error)
	Message  string // Error message
	Err      error  // Wrapped error if any
}

// Error implements the error interface
func (e *ConversionError) Error() string {
	if e.StepIndex > 0 {
		return fmt.Sprintf("test %s, step %d (%s): %s", e.TestName, e.StepIndex, e.StepType, e.Message)
	}
	return fmt.Sprintf("test %s: %s", e.TestName, e.Message)
}

// Unwrap returns the wrapped error
func (e *ConversionError) Unwrap() error {
	return e.Err
}

// NewConversionError creates a new ConversionError with context
func NewConversionError(testName, stepType, message string) *ConversionError {
	return &ConversionError{
		TestName: testName,
		StepType: stepType,
		Message:  message,
	}
}

// NewConversionErrorWithStep creates a new ConversionError with step context
func NewConversionErrorWithStep(testName, stepType string, stepIndex int, message string) *ConversionError {
	return &ConversionError{
		TestName: testName,
		StepType: stepType,
		StepIndex: stepIndex,
		Message:  message,
	}
}

// NewConversionErrorWrap creates a new ConversionError wrapping an existing error
func NewConversionErrorWrap(testName, stepType string, err error, message string) *ConversionError {
	return &ConversionError{
		TestName: testName,
		StepType: stepType,
		Message:  message,
		Err:      err,
	}
}

// NewSkipStep creates a ConvertedStep that skips the step with a clear message.
// This is used for non-critical errors like invalid format that don't prevent
// the rest of the test from being converted.
func NewSkipStep(stepType, reason string) ConvertedStep {
	return ConvertedStep{
		Type:   stepType,
		GoCode: fmt.Sprintf("t.Skipf(\"Step skipped: %s\")", reason),
	}
}

// NewErrorStep creates a ConvertedStep that fails the test with an error message.
// This is used for critical errors that should cause the test to fail.
func NewErrorStep(stepType, reason string) ConvertedStep {
	return ConvertedStep{
		Type:   stepType,
		GoCode: fmt.Sprintf("t.Fatalf(\"Step failed: %s\")", reason),
	}
}


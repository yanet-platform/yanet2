// Package helperproto is the protocol between the native control plane's
// tests and the dataplane test helper.
//
// Both ends speak encoding/gob over the helper's input and output: the
// client sends the options, the helper replies ready with its red zone, and
// then the client sends one request at a time and reads its reply. Both
// sides import this package, so it needs no cgo.
package helperproto

// StorageFD is the helper's descriptor of the storage.
const StorageFD = 3

// Options is the dataplane the helper runs on the storage.
type Options struct {
	Workers  int
	CPMemory uint64
	DPMemory uint64
	// Devices names the dataplane's devices, in index order.
	Devices []string
	// Modules, DeviceTypes and ObjectTypes are the types the dataplane
	// loads.
	Modules     []string
	DeviceTypes []string
	ObjectTypes []string
	Fault       Fault
}

// Op is a request the helper answers.
type Op uint8

const (
	// OpDeliver hands a generation the client published to the workers.
	OpDeliver Op = iota + 1
	// OpRound runs one round of frames on a worker and device.
	OpRound
	// OpTime sets the time the next rounds run at.
	OpTime
	// OpQuit frees the harness and ends the helper.
	OpQuit
)

// Request is a request of the client.
type Request struct {
	Op Op
	// Offset is the storage offset of the generation a delivery hands
	// over.
	Offset uint64
	// Worker, Device and Frames are a round's.
	Worker int
	Device uint16
	Frames [][]byte
	// Time is the nanoseconds since the epoch a time request sets.
	Time int64
	// Marks are the runs of the control-plane zone the client's sanitizer
	// holds unaddressable, sent with deliveries and rounds in
	// AddressSanitizer builds.
	Marks []Range
}

// Range is a run of storage bytes from an offset of the storage.
type Range struct {
	Offset uint64
	Size   uint64
}

// Reply is the helper's answer to the options or a request.
type Reply struct {
	// Err is the text of a refusal, empty when the helper did as asked.
	Err string
	// RedZone is the red zone of the helper's blocks, in the ready reply.
	RedZone uint32
	// Output and Drop are the frames a round put out and dropped.
	Output [][]byte
	Drop   [][]byte
}

// Fault is a failure the client's own tests make the helper show on
// purpose.
//
// The exit status strikes at quit, the red zone at ready and the read past
// the generation at the first delivery; every other fault strikes at the
// first delivery, round or time request.
type Fault string

const (
	// FaultNone runs the helper without a fault.
	FaultNone Fault = ""
	// FaultCrash panics.
	FaultCrash Fault = "crash"
	// FaultLinger exits nonzero, leaving a process it started holding its
	// error output.
	FaultLinger Fault = "linger"
	// FaultHang never answers.
	FaultHang Fault = "hang"
	// FaultExit answers quit and exits nonzero.
	FaultExit Fault = "exit"
	// FaultError refuses the request.
	FaultError Fault = "error"
	// FaultLeak keeps an mbuf of the harness pool.
	FaultLeak Fault = "leak"
	// FaultSanitizer reads unaddressable memory in C.
	FaultSanitizer Fault = "sanitizer"
	// FaultRedZone replies ready with a red zone it does not use.
	FaultRedZone Fault = "red-zone"
	// FaultReadPastGeneration reads the byte past the generation of its
	// first delivery.
	FaultReadPastGeneration Fault = "read-past-generation"
)

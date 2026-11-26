//go:build linux && (amd64 || arm64)

// Command dataplane-helper runs the dataplane test harness as a process of
// its own, for the native control plane's tests.
//
// The client creates the storage, sizes it and passes its descriptor, and
// sends the options; the helper lays the harness out in the storage,
// replies ready with its red zone and then answers one request at a time
// from its input until quit or the end of the input. It runs the harness
// and writes no configuration, so once it is ready the client is the only
// allocator of the control-plane zone.
// A panic aborts it with every stack on its error output.
package main

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"time"

	"golang.org/x/sys/unix"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
)

// holdArg starts the helper as the process the linger fault leaves behind.
const holdArg = "--hold-error-output"

func main() {
	// A panic is a crash of the helper, never a reply.
	debug.SetTraceback("crash")
	if len(os.Args) == 2 && os.Args[1] == holdArg {
		hold()
	}
	os.Exit(run())
}

// hold keeps the error output it inherited open until its input, the
// client's requests, ends, and exits then.
func hold() {
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		report("hold: %v", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// run serves the client and returns the helper's exit status: zero after
// a clean quit, 1 for a failure, 2 for options that do not read and 3 for
// the exit fault.
func run() int {
	decoder := gob.NewDecoder(os.Stdin)
	encoder := gob.NewEncoder(os.Stdout)
	var options helperproto.Options
	if err := decoder.Decode(&options); err != nil {
		report("read options: %v", err)
		return 2
	}
	// The harness tells why it failed in its log.
	dataplaneut.EnableLog("warn")
	m, err := open(&options)
	if err != nil {
		report("%v", err)
		if err := encoder.Encode(&helperproto.Reply{Err: err.Error()}); err != nil {
			report("reply: %v", err)
		}
		return 1
	}
	m.decoder, m.encoder = decoder, encoder
	redZone := uint32(dataplaneut.RedZone)
	if options.Fault == helperproto.FaultRedZone {
		redZone += 64
	}
	if err := encoder.Encode(&helperproto.Reply{RedZone: redZone}); err != nil {
		report("reply: %v", err)
		if err := m.close(); err != nil {
			report("%v", err)
		}
		return 1
	}
	return m.serve()
}

// report writes a line to the helper's error output.
func report(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dataplane helper: "+format+"\n", args...)
}

// helper is the harness running in the client's storage.
type helper struct {
	harness *dataplaneut.Harness
	storage []byte
	fault   helperproto.Fault
	// cpStart is the storage offset of the control-plane zone.
	cpStart uint64
	decoder *gob.Decoder
	encoder *gob.Encoder
}

// open lays the harness out in the storage the descriptor names.
func open(options *helperproto.Options) (*helper, error) {
	file := os.NewFile(helperproto.StorageFD, "storage")
	size := options.CPMemory + options.DPMemory
	storage, err := mapStorage(file, size)
	// The mapping holds the storage from here on.
	if closeErr := file.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close storage: %w", closeErr))
	}
	if err != nil {
		if storage != nil {
			err = errors.Join(err, unix.Munmap(storage))
		}
		return nil, err
	}

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      options.CPMemory,
		DPMemory:      options.DPMemory,
		WorkerCount:   uint64(options.Workers),
		Devices:       options.Devices,
		Modules:       options.Modules,
		DevicesToLoad: options.DeviceTypes,
		ObjectsToLoad: options.ObjectTypes,
		Storage:       storage,
	})
	if err != nil {
		return nil, errors.Join(err, unix.Munmap(storage))
	}
	return &helper{harness: harness, storage: storage, fault: options.Fault, cpStart: options.DPMemory}, nil
}

// mapStorage maps the storage shared, after checking that it holds size
// bytes.
func mapStorage(file *os.File, size uint64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if uint64(info.Size()) < size {
		return nil, fmt.Errorf("storage of %d bytes is shorter than %d", info.Size(), size)
	}
	storage, err := unix.Mmap(int(file.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("map storage: %w", err)
	}
	return storage, nil
}

// serve answers requests until quit and returns the helper's exit status.
func (m *helper) serve() int {
	for {
		var request helperproto.Request
		if err := m.decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				report("the input ended before quit")
			} else {
				report("read request: %v", err)
			}
			if err := m.close(); err != nil {
				report("%v", err)
			}
			return 1
		}
		if request.Op == helperproto.OpQuit {
			return m.quit()
		}

		reply, err := m.answer(&request)
		if err != nil {
			reply = &helperproto.Reply{Err: err.Error()}
		}
		if err := m.encoder.Encode(reply); err != nil {
			report("reply: %v", err)
			if err := m.close(); err != nil {
				report("%v", err)
			}
			return 1
		}
	}
}

// answer answers a request other than quit, after any fault it strikes.
func (m *helper) answer(request *helperproto.Request) (*helperproto.Reply, error) {
	if err := m.inject(); err != nil {
		return nil, err
	}
	switch request.Op {
	case helperproto.OpDeliver:
		poison(m.storage, m.cpStart, request.Marks)
		if m.fault == helperproto.FaultReadPastGeneration {
			m.fault = helperproto.FaultNone
			if err := readPast(m.storage, request.Offset+dataplaneut.GenerationSize); err != nil {
				return nil, err
			}
		}
		return &helperproto.Reply{}, m.harness.Deliver(request.Offset)
	case helperproto.OpRound:
		poison(m.storage, m.cpStart, request.Marks)
		result, err := m.harness.HandleFramesOnDevice(request.Worker, request.Device, request.Frames...)
		if err != nil {
			return nil, err
		}
		return &helperproto.Reply{Output: result.Output, Drop: result.Drop}, nil
	case helperproto.OpTime:
		m.harness.SetCurrentTime(time.Unix(0, request.Time))
		return &helperproto.Reply{}, nil
	default:
		return nil, fmt.Errorf("unknown request %d", request.Op)
	}
}

// inject shows the fault the helper was started with, at its first
// delivery, round or time request; an error refuses that request.
func (m *helper) inject() error {
	fault := m.fault
	switch fault {
	case helperproto.FaultNone, helperproto.FaultExit, helperproto.FaultRedZone, helperproto.FaultReadPastGeneration:
		return nil
	}
	m.fault = helperproto.FaultNone
	switch fault {
	case helperproto.FaultCrash:
		// No core dump: the crash is known and a dump takes time.
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			report("keep the crash from dumping core: %v", err)
		}
		panic("injected crash")
	case helperproto.FaultLinger:
		// The process lives on, holding the error output, until killed or
		// until the client's requests end.
		holder := exec.Command("/proc/self/exe", holdArg)
		holder.Stdin = os.Stdin
		holder.Stderr = os.Stderr
		if err := holder.Start(); err != nil {
			return fmt.Errorf("start a process holding the error output: %w", err)
		}
		report("injected exit leaving a process behind")
		os.Exit(1)
	case helperproto.FaultHang:
		report("injected hang")
		for {
			// The pause ends at every signal the runtime gets.
			_ = unix.Pause()
		}
	case helperproto.FaultError:
		return errors.New("injected error")
	case helperproto.FaultLeak:
		if err := m.harness.LeakMbuf(); err != nil {
			return fmt.Errorf("leak: %w", err)
		}
	case helperproto.FaultSanitizer:
		return readPoisonedFromC()
	}
	return nil
}

// quit frees the harness, refusing quit when mbufs are left outstanding,
// and returns the helper's exit status.
func (m *helper) quit() int {
	outstanding := m.harness.OutstandingMbufs()
	err := m.close()
	if outstanding != 0 {
		err = errors.Join(fmt.Errorf("%d mbufs outstanding at quit", outstanding), err)
	}
	if err != nil {
		report("%v", err)
		if err := m.encoder.Encode(&helperproto.Reply{Err: err.Error()}); err != nil {
			report("reply: %v", err)
		}
		return 1
	}
	if err := m.encoder.Encode(&helperproto.Reply{}); err != nil {
		report("reply: %v", err)
		return 1
	}
	if m.fault == helperproto.FaultExit {
		return 3
	}
	return 0
}

// close frees the harness and unmaps the storage, which stays the
// client's.
func (m *helper) close() error {
	m.harness.Free()
	if err := unix.Munmap(m.storage); err != nil {
		return fmt.Errorf("unmap storage: %w", err)
	}
	return nil
}

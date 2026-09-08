package framework

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type recoveryWriter struct {
	manager                *QEMUManager
	commandWrites          int
	startMarker            string
	endMarker              string
	completeAfterInterrupt bool
	interruptErr           error
}

// Write models command echo, wrapper completion, and interrupt failure.
func (m *recoveryWriter) Write(data []byte) (int, error) {
	if bytes.Equal(data, []byte{3, '\n'}) {
		if m.interruptErr != nil {
			return 0, m.interruptErr
		}
		if m.completeAfterInterrupt {
			m.manager.serialBuffer.WriteString("=130=" + m.endMarker + "\n")
		}
		return len(data), nil
	}

	m.commandWrites++
	m.startMarker = string(regexp.MustCompile(`CMD_START_\d+`).Find(data))
	m.endMarker = string(regexp.MustCompile(`CMD_END_\d+`).Find(data))
	m.manager.serialBuffer.Write(data)
	if m.commandWrites == 1 && !m.completeAfterInterrupt {
		return len(data), nil
	}
	m.manager.serialBuffer.WriteString(m.startMarker + "\n")
	if m.commandWrites > 1 {
		m.manager.serialBuffer.WriteString("later command output\n=0=" + m.endMarker + "\n")
	}
	return len(data), nil
}

type recoveryConnection struct {
	*recoveryWriter
	closed bool
}

// Read reports an idle fake serial console.
func (m *recoveryConnection) Read([]byte) (int, error) {
	return 0, io.EOF
}

// Close records that command execution invalidated the serial console.
func (m *recoveryConnection) Close() error {
	m.closed = true
	return nil
}

// LocalAddr returns the in-memory serial endpoint.
func (m *recoveryConnection) LocalAddr() net.Addr {
	return testAddress("local")
}

// RemoteAddr returns the in-memory guest endpoint.
func (m *recoveryConnection) RemoteAddr() net.Addr {
	return testAddress("guest")
}

// SetDeadline accepts deadlines for the in-memory connection.
func (m *recoveryConnection) SetDeadline(time.Time) error {
	return nil
}

// SetReadDeadline accepts read deadlines for the in-memory connection.
func (m *recoveryConnection) SetReadDeadline(time.Time) error {
	return nil
}

// SetWriteDeadline accepts write deadlines for the in-memory connection.
func (m *recoveryConnection) SetWriteDeadline(time.Time) error {
	return nil
}

type testAddress string

// Network identifies the fake serial transport.
func (m testAddress) Network() string {
	return "test"
}

// String returns the endpoint label.
func (m testAddress) String() string {
	return string(m)
}

// Test_CLIManager_ExecuteCommandWithTimeout_RecoveredShellPreservesTimeout
// verifies that an executed marker restores availability without hiding timeout.
func Test_CLIManager_ExecuteCommandWithTimeout_RecoveredShellPreservesTimeout(t *testing.T) {
	manager := &QEMUManager{
		Name:    "recovery-test",
		Command: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
		log:     zap.NewNop().Sugar(),
	}
	connection := &recoveryConnection{recoveryWriter: &recoveryWriter{
		manager:                manager,
		completeAfterInterrupt: true,
	}}
	manager.serialConn = connection
	manager.setVMReady(true)
	cli := &CLIManager{inner: &cliManagerInner{qemu: manager}, log: zap.NewNop().Sugar()}

	_, err := cli.ExecuteCommandWithTimeout("sleep until interrupted", time.Millisecond)
	require.ErrorIs(t, err, errCommandTimeout)
	require.NotContains(t, err.Error(), "serial recovery failed")
	require.True(t, manager.IsVMReady())
	require.False(t, connection.closed)

	output, err := cli.ExecuteCommandWithTimeout("echo later", time.Second)
	require.NoError(t, err)
	require.Equal(t, "later command output", output)
}

func TestCommandWithMarkers_InterruptPrintsCompletionMarker(t *testing.T) {
	command := commandWithMarkers("kill -INT $$", "CMD_START_TEST", "CMD_END_TEST")
	output, err := exec.Command("bash", "-c", command).CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(output), "CMD_START_TEST")
	require.Contains(t, string(output), "=130=CMD_END_TEST")
}

// Test_CLIManager_ExecuteCommandWithTimeout_EchoWithoutCompletionAbortsSerial
// verifies that wrapper echo alone cannot prove recovery after interruption.
func Test_CLIManager_ExecuteCommandWithTimeout_EchoWithoutCompletionAbortsSerial(t *testing.T) {
	originalTimeout := serialRecoveryTimeout
	serialRecoveryTimeout = 20 * time.Millisecond
	t.Cleanup(func() { serialRecoveryTimeout = originalTimeout })

	manager := &QEMUManager{
		Name:    "echo-test",
		Command: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
		log:     zap.NewNop().Sugar(),
	}
	connection := &recoveryConnection{recoveryWriter: &recoveryWriter{manager: manager}}
	manager.serialConn = connection
	manager.setVMReady(true)
	cli := &CLIManager{inner: &cliManagerInner{qemu: manager}, log: zap.NewNop().Sugar()}

	_, err := cli.ExecuteCommandWithTimeout("sleep forever", time.Millisecond)
	require.ErrorIs(t, err, errCommandTimeout)
	require.ErrorContains(t, err, "serial recovery failed")
	require.False(t, manager.IsVMReady())
	require.True(t, connection.closed)
}

// Test_CLIManager_ExecuteCommandWithTimeout_InterruptWriteFailureAbortsSerial
// verifies that transport failure preserves both timeout and recovery causes.
func Test_CLIManager_ExecuteCommandWithTimeout_InterruptWriteFailureAbortsSerial(t *testing.T) {
	interruptErr := errors.New("serial write failed")
	manager := &QEMUManager{
		Name:    "interrupt-test",
		Command: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
		log:     zap.NewNop().Sugar(),
	}
	connection := &recoveryConnection{recoveryWriter: &recoveryWriter{
		manager:      manager,
		interruptErr: interruptErr,
	}}
	manager.serialConn = connection
	manager.setVMReady(true)
	cli := &CLIManager{inner: &cliManagerInner{qemu: manager}, log: zap.NewNop().Sugar()}

	_, err := cli.ExecuteCommandWithTimeout("sleep forever", time.Millisecond)
	require.ErrorIs(t, err, errCommandTimeout)
	require.ErrorIs(t, err, interruptErr)
	require.False(t, manager.IsVMReady())
	require.True(t, connection.closed)
}

// Test_CLIManager_ExecuteCommandWithTimeout_UnrecoveredShellRejectsLaterCommand
// verifies that failed recovery closes the serial path until an explicit reset.
func Test_CLIManager_ExecuteCommandWithTimeout_UnrecoveredShellRejectsLaterCommand(t *testing.T) {
	originalTimeout := serialRecoveryTimeout
	serialRecoveryTimeout = 20 * time.Millisecond
	t.Cleanup(func() { serialRecoveryTimeout = originalTimeout })

	server, client := net.Pipe()
	var received bytes.Buffer
	var receivedMutex sync.Mutex
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(writerFunc(func(data []byte) (int, error) {
			receivedMutex.Lock()
			defer receivedMutex.Unlock()
			return received.Write(data)
		}), server)
		close(drained)
	}()

	manager := &QEMUManager{
		Name:       "timeout-test",
		Command:    &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
		serialConn: client,
		log:        zap.NewNop().Sugar(),
	}
	manager.setVMReady(true)
	cli := &CLIManager{inner: &cliManagerInner{qemu: manager}, log: zap.NewNop().Sugar()}

	_, err := cli.ExecuteCommandWithTimeout("sleep forever", time.Millisecond)
	require.ErrorIs(t, err, errCommandTimeout)
	require.ErrorContains(t, err, "serial recovery failed")
	require.False(t, manager.IsVMReady())
	<-drained

	receivedMutex.Lock()
	before := received.Len()
	receivedMutex.Unlock()
	_, err = cli.ExecuteCommandWithTimeout("echo unsafe", time.Millisecond)
	require.EqualError(t, err, "VM not ready")
	receivedMutex.Lock()
	require.Equal(t, before, received.Len())
	receivedMutex.Unlock()
	require.NoError(t, server.Close())
}

type writerFunc func([]byte) (int, error)

// Write adapts a function to an output stream.
func (m writerFunc) Write(data []byte) (int, error) {
	return m(data)
}

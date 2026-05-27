// Package fuzzing — compact one-line operation summaries.
//
// These helpers format the operation context that the runner emits at each
// step. They return plain strings so the runner can route them through any
// logger (or os.Stderr) without this file taking a logging dependency.
package fuzzing

import (
	"fmt"
	"strings"
	"time"
)

// FormatUpdate summarises an UpdateVS call. vsCount is the total number of
// virtual servers in the config after the update; vsKey identifies the
// touched VS (typically "ip:port/proto"); opNum is the monotonic operation
// counter the runner maintains.
func FormatUpdate(opNum uint64, vsCount int, dur time.Duration) string {
	return fmt.Sprintf(
		"op=Update num=%d vs_count=%d dur=%s",
		opNum, vsCount, dur,
	)
}

// FormatUpdateVS summarises an UpdateVS call. vsCount is the total number of
// virtual servers in the config after the update; vsKey identifies the
// touched VS (typically "ip:port/proto"); opNum is the monotonic operation
// counter the runner maintains.
func FormatUpdateVS(opNum uint64, vsKey string, vsCount int, dur time.Duration) string {
	return fmt.Sprintf(
		"op=UpdateVS num=%d vs=%s vs_count=%d dur=%s",
		opNum, vsKey, vsCount, dur,
	)
}

// FormatDeleteVS summarises a DeleteVS call. vsCount is the number of
// virtual servers remaining after deletion.
func FormatDeleteVS(opNum uint64, vsKey string, vsCount int, dur time.Duration) string {
	return fmt.Sprintf(
		"op=DeleteVS num=%d vs=%s vs_count=%d dur=%s",
		opNum, vsKey, vsCount, dur,
	)
}

// FormatUpdateReals summarises an UpdateReals call.
func FormatUpdateReals(opNum uint64, vsCount, realUpdates int, dur time.Duration) string {
	return fmt.Sprintf(
		"op=UpdateReals num=%d vs_count=%d real_updates=%d dur=%s",
		opNum, vsCount, realUpdates, dur,
	)
}

// FormatGetState summarises a successful GetState call. vsCount and realCount
// describe the observed state shape so the operator can correlate against
// the expected model size.
func FormatGetState(opNum uint64, vsCount, realCount int, dur time.Duration) string {
	return fmt.Sprintf(
		"op=GetState num=%d vs_count=%d real_count=%d dur=%s",
		opNum, vsCount, realCount, dur,
	)
}

// FormatInitCall summarises an init-time event. The seed is included so each
// replay log is self-describing.
func FormatInitCall(rpc string, seed int64, dur time.Duration) string {
	return fmt.Sprintf(
		"op=Init rpc=%s seed=%d dur=%s",
		rpc, seed, dur,
	)
}

// FormatMismatch summarises a state-comparison mismatch. paths are the
// field paths reported by the comparator (see Task 5); they are joined with
// commas so the whole summary stays on one line.
func FormatMismatch(opNum uint64, paths []string) string {
	return fmt.Sprintf(
		"op=Mismatch num=%d paths=[%s]",
		opNum, strings.Join(paths, ","),
	)
}

// FormatRPCFailure summarises an RPC that returned an error. The operation
// name plus the formatted error gives the operator enough context to grep
// the run for the failing call.
func FormatRPCFailure(opNum uint64, rpc string, err error) string {
	return fmt.Sprintf(
		"op=RPCFailure num=%d rpc=%s err=%v",
		opNum, rpc, err,
	)
}

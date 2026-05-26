// Package fuzzing — single-threaded mutation runner.
//
// Runner drives the balancer2 controlplane through a deterministic stream
// of UpdateVS/DeleteVS/UpdateReals RPCs and validates every successful
// mutation against an independent in-memory model via GetState. The loop
// is intentionally single-threaded: the model is the source of truth and
// must never be mutated concurrently with an in-flight RPC.
//
// The mutation cycle is copy/apply/commit:
//
//  1. Clone the committed model to produce a candidate.
//  2. Apply the generated operation to the candidate.
//  3. Execute the mutation RPC, then GetState, then CompareState against
//     the candidate.
//  4. On success, commit by applying the same operation to the committed
//     model in place. Commit is in-place (not a pointer swap) because
//     OperationGenerator captures the model pointer at construction time;
//     swapping pointers would leave the generator pointing at the stale
//     pre-commit snapshot.
//
// Any failure (RPC error, GetState error, mismatch) returns an error from
// Run; the committed model is never mutated until the candidate has
// been fully validated.

package fuzzing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// Runner orchestrates the fuzzing loop. It owns the committed model, the
// operation generator, the RPC client, the latency stats collector, and a
// log writer; all state is private so callers interact through Run.
type Runner struct {
	cfg            *RuntimeConfig
	corpus         *Corpus
	model          *Model
	gen            *OperationGenerator
	rpc            BalancerRPC
	stats          *LatencyStats
	log            io.Writer
	signals        []os.Signal
	now            func() time.Time
	newOpTicker    func(time.Duration) tickerLike
	newStatsTicker func(time.Duration) tickerLike
	opLimit        uint64
}

// tickerLike is the narrow seam the runner needs from time.Ticker. Tests
// substitute a manual implementation; production wraps time.NewTicker via
// realTicker.
type tickerLike interface {
	C() <-chan time.Time
	Stop()
}

// realTicker is the time.Ticker adapter the production runner uses. It
// owns the underlying *time.Ticker so the runner can Stop deterministically.
type realTicker struct {
	t *time.Ticker
}

func newRealTicker(d time.Duration) tickerLike {
	return &realTicker{t: time.NewTicker(d)}
}

func (m *realTicker) C() <-chan time.Time { return m.t.C }
func (m *realTicker) Stop()               { m.t.Stop() }

// RunnerOption configures a Runner at construction time.
type RunnerOption func(*Runner)

// WithLogger redirects the runner's log output away from the default
// os.Stderr destination. The writer must remain valid for the lifetime
// of Run.
func WithLogger(w io.Writer) RunnerOption {
	return func(r *Runner) { r.log = w }
}

// WithSignals overrides the OS signals the runner installs as graceful
// stop triggers. The default set is SIGINT and SIGTERM.
func WithSignals(sigs ...os.Signal) RunnerOption {
	return func(r *Runner) { r.signals = sigs }
}

// WithOperationLimit caps the number of operations the runner will
// execute before returning nil. A zero limit means "run until cancelled
// or signalled". This is primarily a test hook so the runner can be
// driven through a deterministic, bounded loop without relying on
// timing.
func WithOperationLimit(limit uint64) RunnerOption {
	return func(r *Runner) { r.opLimit = limit }
}

// WithClock overrides the wall-clock used by the runner. Tests inject a
// virtual clock so generated log lines and stats reports are
// deterministic.
func WithClock(now func() time.Time) RunnerOption {
	return func(r *Runner) { r.now = now }
}

// WithOperationTicker overrides the factory used to build the operation
// cadence ticker. Tests pass a manual ticker so each operation can be
// driven by an explicit Fire() call rather than wall time.
func WithOperationTicker(factory func(time.Duration) tickerLike) RunnerOption {
	return func(r *Runner) { r.newOpTicker = factory }
}

// WithStatsTicker overrides the factory used to build the stats cadence
// ticker. Tests use it to fire the stats path on demand.
func WithStatsTicker(factory func(time.Duration) tickerLike) RunnerOption {
	return func(r *Runner) { r.newStatsTicker = factory }
}

// NewRunner builds a Runner around a parsed corpus and an RPC client. It
// constructs the initial model and operation generator from cfg.Seed;
// callers that want a different model must inject it after construction
// is not supported by design — the runner owns its model strictly.
func NewRunner(
	cfg *RuntimeConfig,
	corpus *Corpus,
	rpc BalancerRPC,
	stats *LatencyStats,
	options ...RunnerOption,
) (*Runner, error) {
	if cfg == nil {
		return nil, errors.New("runner: runtime config must not be nil")
	}
	if corpus == nil {
		return nil, errors.New("runner: corpus must not be nil")
	}
	if rpc == nil {
		return nil, errors.New("runner: rpc client must not be nil")
	}
	if stats == nil {
		return nil, errors.New("runner: stats must not be nil")
	}

	model := NewModel(corpus)
	gen := NewOperationGenerator(model, cfg.UpdateVsEvery, cfg.Seed)

	r := &Runner{
		cfg:            cfg,
		corpus:         corpus,
		model:          model,
		gen:            gen,
		rpc:            rpc,
		stats:          stats,
		log:            os.Stderr,
		signals:        []os.Signal{syscall.SIGINT, syscall.SIGTERM},
		now:            time.Now,
		newOpTicker:    newRealTicker,
		newStatsTicker: newRealTicker,
	}
	for _, o := range options {
		o(r)
	}
	return r, nil
}

// Model returns the runner's currently-committed model. Callers must
// treat it as read-only; the runner owns mutation lifecycle.
func (m *Runner) Model() *Model {
	return m.model
}

// Run executes the operation loop. It returns nil on graceful stop (ctx
// cancellation or one of the configured signals) and a non-nil error on
// RPC failure, GetState failure, or state mismatch.
//
// The loop is single-threaded: every operation completes its full
// mutation+GetState+compare+commit cycle before the next one starts.
// Signals are converted to a derived context cancellation so the
// in-flight RPC is allowed to finish (bounded by RequestTimeout) before
// the loop exits.
func (m *Runner) Run(ctx context.Context) error {
	runCtx, cancel := m.installSignalHandler(ctx)
	defer cancel()

	return m.loop(runCtx)
}

// installSignalHandler wires SIGINT/SIGTERM into a derived context. The
// returned cancel must be invoked by the caller to release the signal
// goroutine.
func (m *Runner) installSignalHandler(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(parent)
	if len(m.signals) == 0 {
		return runCtx, cancel
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, m.signals...)
	go func() {
		select {
		case <-runCtx.Done():
		case <-sigCh:
			cancel()
		}
		signal.Stop(sigCh)
	}()
	return runCtx, cancel
}

// loop is the single-threaded mutation cadence. It fires one operation
// per tick of the operation ticker and emits a latency report on each
// tick of the stats ticker. Cancellation is checked before starting a
// new operation; if cancellation arrives mid-operation, the in-flight
// RPC finishes (bounded by RequestTimeout) and the loop exits after the
// candidate is either committed or discarded.
func (m *Runner) loop(ctx context.Context) error {
	opTicker := m.newOpTicker(m.cfg.OperationInterval)
	defer opTicker.Stop()
	statsTicker := m.newStatsTicker(m.cfg.StatsInterval)
	defer statsTicker.Stop()

	var opNum uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-statsTicker.C():
			m.emitStatsReport()
		case <-opTicker.C():
			opNum++
			if err := m.step(ctx, opNum); err != nil {
				return err
			}
			if m.opLimit > 0 && opNum >= m.opLimit {
				return nil
			}
		}
	}
}

// step executes one full mutation cycle. It generates the operation from
// the committed model, applies it to a candidate clone, runs the RPC,
// fetches the new state, compares, and only commits on success.
func (m *Runner) step(ctx context.Context, opNum uint64) error {
	if err := ctx.Err(); err != nil {
		return nil
	}

	op := m.gen.Generate(opNum)
	candidate := m.model.Clone()
	if err := candidate.Apply(op); err != nil {
		return fmt.Errorf("runner: apply op %d to candidate: %w", opNum, err)
	}

	mutationStart := m.now()
	if err := m.executeMutation(ctx, op); err != nil {
		return err
	}
	mutationDur := m.now().Sub(mutationStart)
	m.logOperation(op, candidate, mutationDur)

	stateStart := m.now()
	resp, err := m.rpc.GetState(ctx, &balancerpb.GetStateRequest{ConfigName: m.cfg.ConfigName})
	stateDur := m.now().Sub(stateStart)
	if err != nil {
		m.logf("%s", FormatRPCFailure(opNum, RPCGetState, err))
		return fmt.Errorf("runner: get state for op %d: %w", opNum, err)
	}

	if paths := CompareState(m.cfg, candidate, resp); len(paths) > 0 {
		m.logf("%s", FormatMismatch(opNum, paths))
		return fmt.Errorf("runner: state mismatch on op %d: %v", opNum, paths)
	}

	m.logf(
		"%s",
		FormatGetState(opNum, len(resp.GetStates()[0].GetVs()), countReals(resp), stateDur),
	)
	if err := m.model.Apply(op); err != nil {
		return fmt.Errorf("runner: commit op %d to model: %w", opNum, err)
	}
	return nil
}

// executeMutation dispatches an operation to its matching RPC. The
// committed model is read for context (immutable corpus snapshots,
// active VS lookups) but never mutated; the candidate has already been
// updated by step.
func (m *Runner) executeMutation(ctx context.Context, op Operation) error {
	switch op.Type {
	case OpUpdateVS:
		return m.sendUpdateVS(ctx, op)
	case OpDeleteVS, OpDeleteVSNoop:
		return m.sendDeleteVS(ctx, op)
	case OpUpdateReals:
		return m.sendUpdateReals(ctx, op)
	default:
		return fmt.Errorf("runner: unknown operation type %d for op %d", op.Type, op.OpNum)
	}
}

func (m *Runner) sendUpdateVS(ctx context.Context, op Operation) error {
	if op.UpdateVS == nil {
		return fmt.Errorf("runner: update_vs payload missing for op %d", op.OpNum)
	}
	vs := m.toVsConfig(op.UpdateVS)
	req := &balancerpb.UpdateVSRequest{
		ConfigName: m.cfg.ConfigName,
		Vs:         []*balancerpb.VsConfig{vs},
	}
	if _, err := m.rpc.UpdateVS(ctx, req); err != nil {
		m.logf("%s", FormatRPCFailure(op.OpNum, RPCUpdateVS, err))
		return fmt.Errorf("runner: update_vs op %d: %w", op.OpNum, err)
	}
	return nil
}

func (m *Runner) sendDeleteVS(ctx context.Context, op Operation) error {
	if op.DeleteVS == nil {
		return fmt.Errorf("runner: delete_vs payload missing for op %d", op.OpNum)
	}
	ids := make([]*balancerpb.VsIdentifier, 0, len(op.DeleteVS.Keys))
	for _, k := range op.DeleteVS.Keys {
		ids = append(ids, vsKeyToIdentifier(k))
	}
	req := &balancerpb.DeleteVSRequest{
		ConfigName: m.cfg.ConfigName,
		Vs:         ids,
	}
	if _, err := m.rpc.DeleteVS(ctx, req); err != nil {
		m.logf("%s", FormatRPCFailure(op.OpNum, RPCDeleteVS, err))
		return fmt.Errorf("runner: delete_vs op %d: %w", op.OpNum, err)
	}
	return nil
}

func (m *Runner) sendUpdateReals(ctx context.Context, op Operation) error {
	if op.UpdateReals == nil {
		return fmt.Errorf("runner: update_reals payload missing for op %d", op.OpNum)
	}

	updates := make([]*balancerpb.RealUpdate, 0)
	for _, batch := range op.UpdateReals.Batches {
		if !m.model.IsActive(batch.Key) {
			continue
		}
		for _, u := range batch.Updates {
			updates = append(updates, &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs:   vsKeyToIdentifier(batch.Key),
					Real: realKeyToIdentifier(u.Key),
				},
				Enable: cloneBoolPtr(u.Enabled),
				Weight: cloneUint32Ptr(u.Weight),
			})
		}
	}
	req := &balancerpb.UpdateRealsRequest{
		ConfigName: m.cfg.ConfigName,
		Updates:    updates,
	}
	if _, err := m.rpc.UpdateReals(ctx, req); err != nil {
		m.logf("%s", FormatRPCFailure(op.OpNum, RPCUpdateReals, err))
		return fmt.Errorf("runner: update_reals op %d: %w", op.OpNum, err)
	}
	return nil
}

// toVsConfig converts the runner's UpdateVS payload into a balancerpb
// VsConfig. Identity (Id) is taken from the original VS captured at
// parser time so the immutable address/port/proto triple never changes;
// scheduler, flags, allowed sources and reals come from the freshly
// generated payload.
func (m *Runner) toVsConfig(p *UpdateVSPayload) *balancerpb.VsConfig {
	original := m.model.OriginalVS(p.Key)
	id := vsKeyToIdentifier(p.Key)
	if original != nil {
		id.Addr = keyIPBytes(original.Key.IP)
		id.Port = uint32(original.Key.Port)
		id.Proto = original.Key.Proto
	}
	reals := make([]*balancerpb.RealConfig, 0, len(p.Reals))
	for _, r := range p.Reals {
		weight := r.Weight
		enabled := r.Enabled
		rc := &balancerpb.RealConfig{
			Id:      realKeyToIdentifier(r.Key),
			Weight:  &weight,
			Enabled: &enabled,
		}
		rc.Src = sourceForRealMember(r.Key, m.model.OriginalReal(p.Key, r.Key))
		reals = append(reals, rc)
	}
	return &balancerpb.VsConfig{
		Id:             id,
		Scheduler:      p.Scheduler,
		Flags:          flagsToProto(p.Flags),
		AllowedSources: cidrsToAllowedSources(p.AllowedSources),
		Reals:          reals,
	}
}

// vsKeyToIdentifier builds a fresh VsIdentifier whose Addr slice is an
// independent copy of the key's IP bytes. Protobuf messages must not
// alias caller-owned buffers across mutation RPCs.
func vsKeyToIdentifier(key VsKey) *balancerpb.VsIdentifier {
	return &balancerpb.VsIdentifier{
		Addr:  keyIPBytes(key.IP),
		Port:  uint32(key.Port),
		Proto: key.Proto,
	}
}

// realKeyToIdentifier builds a fresh RelativeRealIdentifier with an
// independent address copy.
func realKeyToIdentifier(key RealKey) *balancerpb.RelativeRealIdentifier {
	return &balancerpb.RelativeRealIdentifier{
		Ip:   keyIPBytes(key.IP),
		Port: uint32(key.Port),
	}
}

func sourceForRealMember(key RealKey, orig *RealServer) *filterpb.IPNet {
	if orig != nil && orig.Bindto != nil && sameAddrFamily(orig.Bindto, key.IP) {
		return &filterpb.IPNet{
			Addr: append([]byte(nil), orig.Bindto...),
			Mask: append([]byte(nil), orig.BindtoMask...),
		}
	}
	// Balancer backend requires source for every real.
	if v4 := net.IP(key.IP[:]).To4(); v4 != nil {
		return &filterpb.IPNet{
			Addr: append([]byte(nil), v4...),
			Mask: []byte{0xff, 0xff, 0xff, 0xff},
		}
	}
	mask := make([]byte, net.IPv6len)
	for idx := range mask {
		mask[idx] = 0xff
	}
	return &filterpb.IPNet{
		Addr: keyIPBytes(key.IP),
		Mask: mask,
	}
}

// flagsToProto translates the runner's VsFlags into a freshly-allocated
// balancerpb.VsFlags. Returning a new message on every call avoids
// aliasing the proto message's embedded state across RPCs.
func flagsToProto(flags VsFlags) *balancerpb.VsFlags {
	return &balancerpb.VsFlags{
		Gre:    flags.Gre,
		FixMss: flags.FixMss,
		PureL3: flags.PureL3,
	}
}

// cidrsToAllowedSources translates the runner's CIDR slice into a
// balancerpb AllowedSources slice. Each CIDR becomes a single
// AllowedSources entry containing exactly one IPNet — matching how the
// operation generator emits the source network list.
func cidrsToAllowedSources(cidrs []CIDR) []*balancerpb.AllowedSources {
	if len(cidrs) == 0 {
		return nil
	}
	out := make([]*balancerpb.AllowedSources, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{{
				Addr: append([]byte(nil), c.Addr...),
				Mask: append([]byte(nil), c.Mask...),
			}},
		})
	}
	return out
}

// cloneBoolPtr returns an independent *bool so the protobuf message does
// not alias the runner's RealUpdate.
func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

// cloneUint32Ptr returns an independent *uint32 so the protobuf message
// does not alias the runner's RealUpdate.
func cloneUint32Ptr(src *uint32) *uint32 {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

// logOperation emits the compact per-operation summary. The candidate
// model is the post-mutation expected state, so its ActiveCount reflects
// what the controlplane should report next.
func (m *Runner) logOperation(op Operation, candidate *Model, dur time.Duration) {
	switch op.Type {
	case OpUpdateVS:
		m.logf(
			"%s",
			FormatUpdateVS(op.OpNum, formatVsKey(op.UpdateVS.Key), candidate.ActiveCount(), dur),
		)
	case OpDeleteVS:
		key := ""
		if len(op.DeleteVS.Keys) > 0 {
			key = formatVsKey(op.DeleteVS.Keys[0])
		}
		m.logf("%s", FormatDeleteVS(op.OpNum, key, candidate.ActiveCount(), dur))
	case OpDeleteVSNoop:
		m.logf("%s %s", FormatDeleteVS(op.OpNum, "", candidate.ActiveCount(), dur),
			DeleteVSNoopSummary(op.DeleteVS.ActiveCount, op.DeleteVS.MinActive))
	case OpUpdateReals:
		batchCount := len(op.UpdateReals.Batches)
		updateCount := 0
		for _, batch := range op.UpdateReals.Batches {
			updateCount += len(batch.Updates)
		}
		m.logf("%s", FormatUpdateReals(op.OpNum, batchCount, updateCount, dur))
	}
}

// emitStatsReport writes the current window's latency snapshot to the
// log. Quiet windows (no samples on any op) produce no output so log
// volume tracks activity.
func (m *Runner) emitStatsReport() {
	reports := m.stats.Report()
	if len(reports) == 0 {
		return
	}
	for _, r := range reports {
		m.logf(
			"stats op=%s count=%d errors=%d avg=%s p50=%s p90=%s p95=%s p99=%s max=%s cum_count=%d cum_errors=%d",
			r.Op,
			r.Count,
			r.Errors,
			r.Avg,
			r.P50,
			r.P90,
			r.P95,
			r.P99,
			r.Max,
			r.CumulativeCount,
			r.CumulativeErrors,
		)
	}
}

// logf writes one formatted line to the runner's log destination. The
// log seam exists so tests can capture output without intercepting
// os.Stderr.
func (m *Runner) logf(format string, args ...any) {
	if m.log == nil {
		return
	}
	fmt.Fprintf(m.log, format+"\n", args...)
}

// countReals sums the per-VS real counts in a GetState response. It is
// used purely for the operator-facing summary line; correctness is
// validated by CompareState, not by this count.
func countReals(resp *balancerpb.GetStateResponse) int {
	total := 0
	for _, state := range resp.GetStates() {
		for _, vs := range state.GetVs() {
			total += len(vs.GetReals())
		}
	}
	return total
}

package bird_adapter

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"

	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
)

// importState is the step of its lifecycle an import has reached.
type importState int

const (
	// importPreparing opens the connection and the first stream.
	importPreparing importState = iota
	// importWaiting holds a prepared import, which sends nothing, until the
	// import it replaces has exited.
	importWaiting
	// importRunning runs an attempt that feeds the RIB.
	importRunning
	// importBackoff waits out the delay before the next attempt.
	importBackoff
	// importReopening opens the stream of the next attempt.
	importReopening
	// importReleasing closes the connection.
	importReleasing
	// importReleased has exited; an import without sockets stays listed in
	// it.
	importReleased
)

// lifecycleImport is the control loop's record of one import.
//
// Only the loop reads or writes it. A task takes the values it needs when
// it starts and reports back with one event naming the generation.
type lifecycleImport struct {
	Gen   uint64
	Name  string
	State importState
	// Stopping is set once the import is replaced or the service closes.
	//
	// The end of its task then releases it. A waiting import has no task:
	// it is released once the import it replaces has exited, or at once
	// when the service closes.
	Stopping bool
	// Ran is set once the import has taken over its name.
	Ran bool
	// Ctx parents every task and stream of the import, and Cancel stops
	// them.
	Ctx    context.Context
	Cancel context.CancelFunc
	// Setup takes the reply of the setup that created the import, nil once
	// sent.
	Setup chan<- error
	// Holder owns the connection, from the end of the prepare on.
	Holder *importHolder
	// Attempt is the stream the next attempt runs on, opened by the prepare
	// or by the reopen.
	Attempt   *importAttempt
	Retry     *backoff.ExponentialBackOff
	StartedAt time.Time
	// Predecessor is the import this one waits for, and Successor the
	// prepared import waiting for this one to exit.
	Predecessor *lifecycleImport
	Successor   *lifecycleImport
	Log         *zap.Logger
}

// Holding returns the import that holds the name while this one waits to
// replace it, nil when none does.
//
// Imports superseded while they waited never held the name and are skipped.
func (m *lifecycleImport) Holding() *lifecycleImport {
	holding := m.Predecessor
	for holding != nil && !holding.Ran {
		holding = holding.Predecessor
	}
	return holding
}

// setupRequest asks the control loop to set up an import.
type setupRequest struct {
	Params ImportParams
	// Abandoned is closed once the caller has stopped waiting for the
	// reply.
	Abandoned <-chan struct{}
	// Reply takes the one reply without blocking the loop.
	Reply chan error
}

// listRequest asks the control loop for the listed sessions.
type listRequest struct {
	Reply chan []*adapterpb.SessionInfo
}

// importEventKind names the task an event reports the end of.
type importEventKind int

const (
	eventPrepared importEventKind = iota
	eventAttemptEnded
	eventRetryDue
	eventReopened
	eventReleased
)

// importEvent is the one report of a task of the import Gen.
type importEvent struct {
	Gen  uint64
	Kind importEventKind
	// Holder is the prepared import, set by a prepare that got as far as a
	// connection.
	Holder *importHolder
	// Attempt is the stream a prepare or a reopen opened.
	Attempt *importAttempt
	Err     error
}

// run is the control loop: it owns every import and makes every decision,
// until the service has closed and every import has exited.
//
// It never blocks outside its select: what takes time runs in a task, and
// every reply goes to a channel with room for it. Every task belongs to an
// import the loop still holds, so a task's event always finds the loop.
func (m *AdapterService) run() {
	defer close(m.stopped)

	for !m.closing || len(m.live) > 0 {
		select {
		case request := <-m.setups:
			m.onSetup(request)
		case request := <-m.lists:
			request.Reply <- m.sessions()
		case <-m.closes:
			m.onClose()
		case event := <-m.events:
			m.onEvent(event)
		}
	}
}

// onSetup starts a setup, or queues it behind the setup of the same name
// still preparing.
func (m *AdapterService) onSetup(request setupRequest) {
	if m.closing {
		request.Reply <- errServiceClosed
		return
	}

	name := request.Params.Name
	if waiting, ok := m.queue[name]; ok {
		m.queue[name] = append(waiting, request)
		return
	}
	m.queue[name] = nil
	m.startPrepare(request)
}

// startNextSetup starts the oldest queued setup of name whose caller still
// waits, or marks the name idle.
func (m *AdapterService) startNextSetup(name string) {
	waiting := m.queue[name]
	for len(waiting) > 0 {
		request := waiting[0]
		waiting = waiting[1:]
		select {
		case <-request.Abandoned:
			continue
		default:
		}

		m.queue[name] = waiting
		m.startPrepare(request)
		return
	}
	delete(m.queue, name)
}

// onClose refuses new setups and stops every import, once.
func (m *AdapterService) onClose() {
	if m.closing {
		return
	}
	m.closing = true

	for _, waiting := range m.queue {
		for _, request := range waiting {
			request.Reply <- errServiceClosed
		}
	}
	clear(m.queue)

	for _, imp := range m.live {
		m.stop(imp)
		// A waiting import sends nothing, so it may go before the import it
		// replaces.
		if imp.State == importWaiting {
			m.reply(imp, errServiceClosed)
			m.release(imp)
		}
	}
}

// sessions returns the newest import of every name, each with the import
// that still holds the name while it waits.
func (m *AdapterService) sessions() []*adapterpb.SessionInfo {
	sessions := make([]*adapterpb.SessionInfo, 0, len(m.imports))
	for name, imp := range m.imports {
		session := imp.Holder.ToSessionInfo(name)
		if holding := imp.Holding(); holding != nil {
			session.Replacing = holding.Holder.ToSessionInfo(name)
		}
		sessions = append(sessions, session)
	}
	return sessions
}

// onEvent passes the end of a task to the import it belongs to.
//
// An event is ignored unless its import is still held in the state the
// task runs in.
func (m *AdapterService) onEvent(event importEvent) {
	imp, ok := m.live[event.Gen]
	if !ok {
		return
	}

	switch {
	case event.Kind == eventPrepared && imp.State == importPreparing:
		m.onPrepared(imp, event)
	case event.Kind == eventAttemptEnded && imp.State == importRunning:
		m.onAttemptEnded(imp, event.Err)
	case event.Kind == eventRetryDue && imp.State == importBackoff:
		m.onRetryDue(imp)
	case event.Kind == eventReopened && imp.State == importReopening:
		m.onReopened(imp, event)
	case event.Kind == eventReleased && imp.State == importReleasing:
		m.onReleased(imp)
	}
}

// startPrepare opens the connection and the first stream of a new import.
func (m *AdapterService) startPrepare(request setupRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	m.lastGen++
	imp := &lifecycleImport{
		Gen:    m.lastGen,
		Name:   request.Params.Name,
		State:  importPreparing,
		Ctx:    ctx,
		Cancel: cancel,
		Setup:  request.Reply,
		Log:    m.log.With(zap.String("config", request.Params.Name)),
	}
	m.live[imp.Gen] = imp

	gen, params := imp.Gen, request.Params
	go func() {
		holder, attempt, err := m.prepareImport(ctx, params)
		m.events <- importEvent{
			Gen:     gen,
			Kind:    eventPrepared,
			Holder:  holder,
			Attempt: attempt,
			Err:     err,
		}
	}()
}

// onPrepared installs a prepared import, or releases one that failed or
// came too late.
//
// Setups of one name wait only for the prepare before them.
func (m *AdapterService) onPrepared(imp *lifecycleImport, event importEvent) {
	imp.Holder = event.Holder
	imp.Attempt = event.Attempt
	m.startNextSetup(imp.Name)

	switch {
	case event.Err != nil:
		m.reply(imp, event.Err)
		m.release(imp)
	case m.closing:
		m.reply(imp, errServiceClosed)
		m.release(imp)
	default:
		m.install(imp)
	}
}

// install makes imp the import of its name, at once when no import of the
// name runs, or once the running one has exited.
func (m *AdapterService) install(imp *lifecycleImport) {
	current := m.imports[imp.Name]
	m.imports[imp.Name] = imp
	if current == nil || current.State == importReleased {
		m.start(imp, current != nil)
		return
	}

	imp.State = importWaiting
	imp.Predecessor = current
	current.Successor = imp
	m.stop(current)
}

// start lets imp take over its name: it answers its setup and runs the
// first attempt, or releases an import without sockets at once.
func (m *AdapterService) start(imp *lifecycleImport, replaced bool) {
	imp.Ran = true
	m.replyReady(imp, replaced)

	if imp.Attempt == nil {
		imp.Log.Info("bird import is disabled, no sockets configured")
		m.release(imp)
		return
	}

	imp.Retry = backoff.NewExponentialBackOff()
	imp.Retry.MaxInterval = importRetryMaxDelay
	imp.Retry.Reset()
	m.runAttempt(imp)
}

// runAttempt feeds the RIB on the stream imp has opened.
func (m *AdapterService) runAttempt(imp *lifecycleImport) {
	imp.State = importRunning
	imp.StartedAt = time.Now()

	gen, ctx, holder, attempt, log := imp.Gen, imp.Ctx, imp.Holder, imp.Attempt, imp.Log
	imp.Attempt = nil
	go func() {
		err := holder.RunAttempt(attempt)
		if ctx.Err() != nil {
			log.Info("stopped BIRD import")
		}
		m.events <- importEvent{Gen: gen, Kind: eventAttemptEnded, Err: err}
	}()
}

// onAttemptEnded waits before the next attempt, or releases a stopped
// import.
func (m *AdapterService) onAttemptEnded(imp *lifecycleImport, err error) {
	if imp.Stopping {
		m.release(imp)
		return
	}

	// The route operator closes the stream itself only when another writer
	// of the same RIB has taken the session over, so that ending never
	// counts as a healthy run.
	if errors.Is(err, errFeedRIBClosedByServer) {
		imp.Log.Warn("route operator closed the BIRD import stream, another writer of the same RIB likely superseded it",
			zap.Error(err),
		)
	} else {
		imp.Log.Warn("bird import attempt ended, restarting",
			zap.Error(err),
		)
		if time.Since(imp.StartedAt) >= importHealthyUptime {
			imp.Retry.Reset()
		}
	}

	m.waitRetry(imp)
}

// waitRetry waits out the next retry delay of imp, or until it stops.
func (m *AdapterService) waitRetry(imp *lifecycleImport) {
	imp.State = importBackoff

	gen, ctx := imp.Gen, imp.Ctx
	delay := min(imp.Retry.NextBackOff(), importRetryMaxDelay)
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		m.events <- importEvent{Gen: gen, Kind: eventRetryDue}
	}()
}

// onRetryDue opens the stream of the next attempt, or releases a stopped
// import.
func (m *AdapterService) onRetryDue(imp *lifecycleImport) {
	if imp.Stopping {
		m.release(imp)
		return
	}

	imp.State = importReopening
	gen, ctx, holder := imp.Gen, imp.Ctx, imp.Holder
	go func() {
		attempt, err := holder.OpenAttempt(ctx)
		m.events <- importEvent{Gen: gen, Kind: eventReopened, Attempt: attempt, Err: err}
	}()
}

// onReopened runs the next attempt, waits again after a failed reopen, or
// releases a stopped import with the stream it got.
func (m *AdapterService) onReopened(imp *lifecycleImport, event importEvent) {
	switch {
	case imp.Stopping:
		// Releasing cancels the import's context, which the new stream's
		// context derives from.
		m.release(imp)
	case event.Err != nil:
		imp.Log.Warn("failed to open the BIRD import stream",
			zap.Error(event.Err),
		)
		m.waitRetry(imp)
	default:
		imp.Attempt = event.Attempt
		m.runAttempt(imp)
	}
}

// stop marks the import stopping and cancels its task, whose end then
// releases it.
//
// A waiting import has no task: the exit of the import it replaces, or the
// close, releases it.
func (m *AdapterService) stop(imp *lifecycleImport) {
	imp.Stopping = true
	imp.Cancel()
}

// release closes the connection of imp; an import that never got one exits
// at once.
func (m *AdapterService) release(imp *lifecycleImport) {
	imp.Cancel()
	imp.State = importReleasing
	if imp.Holder == nil {
		m.onReleased(imp)
		return
	}

	gen, holder := imp.Gen, imp.Holder
	go func() {
		_ = holder.Close()
		m.events <- importEvent{Gen: gen, Kind: eventReleased}
	}()
}

// onReleased forgets an exited import and lets the import waiting for it go
// on, unless the service closes.
//
// A successor superseded while it waited never takes over: it is released
// in turn, keeping the one after it waiting until then.
func (m *AdapterService) onReleased(imp *lifecycleImport) {
	delete(m.live, imp.Gen)
	imp.State = importReleased
	successor := imp.Successor
	imp.Successor = nil
	if successor == nil || m.closing {
		return
	}
	successor.Predecessor = nil

	if successor.Stopping {
		m.replyReady(successor, true)
		m.release(successor)
		return
	}
	m.start(successor, true)
}

// replyReady answers the setup of imp once nothing it replaces runs.
func (m *AdapterService) replyReady(imp *lifecycleImport, replaced bool) {
	if replaced && imp.Setup != nil {
		m.log.Info("replaced existing BIRD import",
			zap.String("config", imp.Name),
		)
	}
	m.reply(imp, nil)
}

// reply sends the one reply of the setup that created imp.
func (m *AdapterService) reply(imp *lifecycleImport, err error) {
	if imp.Setup == nil {
		return
	}
	imp.Setup <- err
	imp.Setup = nil
}

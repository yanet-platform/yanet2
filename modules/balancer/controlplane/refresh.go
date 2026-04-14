package balancer

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/go/relptr"
)

type Refresher struct {
	balancer      *Balancer
	mu            *sync.Mutex
	parentCtx     context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	refreshPeriod time.Duration
}

func NewRefresher(balancer *Balancer, mu *sync.Mutex) *Refresher {
	return &Refresher{
		balancer:      balancer,
		mu:            mu,
		refreshPeriod: balancer.config.State.RefreshPeriod.AsDuration(),
	}
}

func (r *Refresher) Run(ctx context.Context) {
	if r.refreshPeriod == 0 {
		return
	}
	r.parentCtx = ctx
	derived, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(r.refreshPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-derived.Done():
				return
			case <-ticker.C:
				r.mu.Lock()
				r.refresh()
				r.mu.Unlock()
			}
		}
	}()
}

func (r *Refresher) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel = nil
}

func (r *Refresher) UpdateRefreshPeriod(period time.Duration) {
	if period == r.refreshPeriod {
		return
	}
	parentCtx := r.parentCtx
	r.Stop()
	r.refreshPeriod = period
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	r.Run(parentCtx)
}

func (r *Refresher) refresh() {
	b := r.balancer
	ph := b.handler
	st := relptr.Deref(&ph.Session_table)
	if st == nil {
		return
	}
	workers := st.Workers
	now := time.Now()

	services := relptr.Slice(&ph.Vs, ph.Vs_count)

	var totalActiveSessions uint64
	for vsIdx := range services {
		vs := &services[vsIdx]
		if vs.isRemoved() {
			continue
		}
		active := vsCalcEffectiveWeights(
			vs,
			workers,
			now,
			float64(ph.Wlc_power),
			uint32(ph.Wlc_max_weight),
		)
		totalActiveSessions += active
		if vs.isWLC() {
			if err := vs.updateRealSelector(&ph.Rcu, b.agent); err != nil {
				b.log.Errorw("failed to update real selector", "vs", vs, "error", err)
			}
		}
	}

	for {
		capacity := uint64(st.capacity())
		if capacity > 0 &&
			float64(
				totalActiveSessions,
			) > float64(
				capacity,
			)*float64(
				ph.Session_table_max_load_factor,
			) {
			newSize := int(capacity * 2)
			if err := b.handler.resizeSessionTable(st, newSize, now); err != nil {
				b.log.Errorw("failed to resize session table", "error", err)
				break
			}
		} else {
			break
		}
	}
}

// vsCalcEffectiveWeights computes active session count for the VS and, when
// the VS has WLC enabled, updates effective_weight on each enabled real using
// the formula from common.proto:
//
//	ratio      = (real_sessions * total_weight) / (total_sessions * real_weight)
//	wlc_factor = max(1.0, power * (1.0 - ratio))
//	eff_weight = min(real_weight * wlc_factor, max_weight)
//
// It returns the total number of active sessions across all non-removed reals.
func vsCalcEffectiveWeights(
	vs *VS,
	workers uint32,
	now time.Time,
	power float64,
	maxWeight uint32,
) uint64 {
	reals := relptr.Slice(&vs.Reals, vs.Reals_count)

	// First pass: gather session counts and weight sums.
	type realInfo struct {
		sessions uint64
	}
	infos := make([]realInfo, len(reals))

	var totalSessions uint64
	var totalWeight uint64
	for i := range reals {
		r := &reals[i]
		if r.isRemoved() {
			continue
		}
		active, _ := r.sessions(workers, now)
		infos[i].sessions = active
		totalSessions += active
		if r.isEnabled() {
			totalWeight += uint64(r.Weight)
		}
	}

	if !vs.isWLC() {
		return totalSessions
	}

	// Second pass: update effective weights.
	for i := range reals {
		r := &reals[i]
		if r.isRemoved() || !r.isEnabled() {
			continue
		}
		if r.Weight == 0 {
			r.Effective_weight = 0
			continue
		}

		var eff float64
		if totalSessions == 0 || totalWeight == 0 {
			// No sessions yet: treat as fully unloaded.
			eff = float64(r.Weight)
		} else {
			ratio := float64(infos[i].sessions) * float64(totalWeight) /
				(float64(totalSessions) * float64(r.Weight))
			ratio = math.Min(ratio, 1.0)
			wlcFactor := math.Max(1.0, power*(1.0-ratio))
			eff = float64(r.Weight) * wlcFactor
		}

		if maxWeight > 0 {
			eff = math.Min(eff, float64(maxWeight))
		}
		r.Effective_weight = uint32(eff)
	}

	return totalSessions
}

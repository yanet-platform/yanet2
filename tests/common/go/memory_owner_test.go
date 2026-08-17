package memoryowner

import (
	"sync"
	"testing"
	"unsafe"
)

func TestOwnerGrowsOnMissAndReturnsToBaseline(t *testing.T) {
	f := newOwnerFixture(1 << 20)
	defer f.fini()

	sizes := []uint64{8, 24, 96, 480, 1000, 2040, 3000}
	var blocks []unsafe.Pointer
	var heldBytes uint64
	for range 4 {
		for _, sz := range sizes {
			p := f.balloc(sz)
			if p == nil {
				t.Fatalf("alloc of %d bytes failed", sz)
			}
			blocks = append(blocks, p)
			heldBytes += reqBlock(sz)
		}
	}
	if f.arenaCount() == 0 {
		t.Fatal("owner never borrowed an arena")
	}

	ingested := f.ingestedTotal()
	if ingested < heldBytes {
		t.Fatalf("ingested %d bytes cannot hold %d allocated", ingested, heldBytes)
	}

	for i, p := range blocks {
		f.bfree(p, sizes[i%len(sizes)])
	}
	if got := f.ownerFreeSize(); got != ingested {
		t.Fatalf("owner free size after round-trip = %d, want ingested %d", got, ingested)
	}

	f.releaseAll()
	if got := f.freeSize(); got != f.baseline {
		t.Fatalf("parent free size after release = %d, want baseline %d", got, f.baseline)
	}
}

func TestOwnerExhaustionIsTruthful(t *testing.T) {
	granule := granuleBlock()
	f := newOwnerFixture(granule)
	defer f.fini()

	req := uint64(8<<10) - 2*redZone()
	expected := granule / reqBlock(req)

	count := 0
	for f.balloc(req) != nil {
		count++
	}
	if count != int(expected) {
		t.Fatalf("single-granule parent yielded %d blocks, want %d", count, expected)
	}
	if got := f.ownerFreeSize(); got != 0 {
		t.Fatalf("owner reported exhaustion with %d free bytes", got)
	}

	f.releaseAll()
	if got := f.freeSize(); got != f.baseline {
		t.Fatalf("parent free size after release = %d, want baseline %d", got, f.baseline)
	}
}

func TestOwnerGrowthFailurePropagates(t *testing.T) {
	f := newOwnerFixture(granuleBlock() - 4096)
	defer f.fini()

	if p := f.balloc(16); p != nil {
		t.Fatal("alloc must fail when the parent cannot fund growth")
	}
	if f.arenaCount() != 0 {
		t.Fatalf("failed growth tracked %d arenas", f.arenaCount())
	}
	if got := f.ownerFreeSize(); got != 0 {
		t.Fatalf("owner free size after failed growth = %d, want 0", got)
	}
	if got := f.freeSize(); got != f.baseline {
		t.Fatalf("parent free size after failed growth = %d, want baseline %d", got, f.baseline)
	}
}

func TestConcurrentMissesBorrowNoSurplus(t *testing.T) {
	granule := granuleBlock()
	f := newOwnerFixture(1 << 20)
	defer f.fini()

	req := uint64(8<<10) - 2*redZone()
	perGranule := int(granule / reqBlock(req))
	goroutines := 4
	perGoroutine := perGranule / 2
	total := goroutines * perGoroutine

	var wg sync.WaitGroup
	held := make([][]unsafe.Pointer, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				p := f.balloc(req)
				if p == nil {
					t.Errorf("goroutine %d: allocation failed before exhaustion", g)
					return
				}
				held[g] = append(held[g], p)
			}
		}()
	}
	wg.Wait()

	wantGranules := (total + perGranule - 1) / perGranule
	if got := f.arenaCount(); got != wantGranules {
		t.Fatalf("concurrent misses borrowed %d granules, want the minimum %d", got, wantGranules)
	}

	for g := range held {
		for _, p := range held[g] {
			f.bfree(p, req)
		}
	}
	f.releaseAll()
	if got := f.freeSize(); got != f.baseline {
		t.Fatalf("parent free size after release = %d, want baseline %d", got, f.baseline)
	}
}

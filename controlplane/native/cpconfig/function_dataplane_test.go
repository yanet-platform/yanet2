//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"fmt"
	"regexp"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// updateFunctions publishes the functions, delivering the contexts to
// the workers.
func updateFunctions(t testing.TB, memory *publishedFixture, functions ...cpconfig.FunctionConfig) error {
	t.Helper()
	return cpconfig.UpdateFunctions(memory.Instance(), functions, memory.deliver())
}

// chainOf returns a chain of the name running the blackhole
// configurations of the names, in order.
func chainOf(name string, weight uint64, modules ...string) cpconfig.FunctionChainConfig {
	chain := cpconfig.FunctionChainConfig{Chain: cpconfig.ChainConfig{Name: name}, Weight: weight}
	for _, module := range modules {
		chain.Chain.Modules = append(chain.Chain.Modules, cpconfig.ChainModuleConfig{Type: "blackhole", Name: module})
	}
	return chain
}

// counterTotal returns the first values of a counter of the storages the
// tags select in the published generation, summed over the workers.
func counterTotal(t *testing.T, memory *publishedFixture, name string, tags ...zone.CounterTag) uint64 {
	t.Helper()
	pinned, err := memory.Instance().Pin()
	require.NoError(t, err)
	defer pinned.Release()
	groups, err := pinned.Counters(tags)
	require.NoError(t, err)
	total := uint64(0)
	found := false
	for _, group := range groups {
		for _, counter := range group.Counters {
			if counter.Name != name {
				continue
			}
			found = true
			for _, values := range counter.Values {
				total += values[0]
			}
		}
	}
	require.True(t, found, "a storage of the tags %v counts %q", tags, name)
	return total
}

// publishedFunction returns the function of a name the published
// generation holds.
func publishedFunction(t *testing.T, memory *publishedFixture, name string) *layout.CpFunction {
	t.Helper()
	functions := &memory.Instance().Cp.CpConfigGen.Get().FunctionRegistry.Registry
	for _, item := range registry.Items(functions) {
		if function := registry.Function(item); shm.String(function.Name[:]) == name {
			return function
		}
	}
	t.Fatalf("the published generation holds no function %q", name)
	return nil
}

// Test_UpdateFunctions_DataplaneRunsGoFunction verifies that the dataplane
// runs weighted chains Go published in place of a published function.
//
// The chains' modules together drop every packet, the function's counters
// continue from the replaced function's, and the storage of the chain of
// the replaced chain's name keeps the values counted before the update.
func Test_UpdateFunctions_DataplaneRunsGoFunction(t *testing.T) {
	memory := newPublishedFixture(t)
	handlePackets(t, memory, icmpPackets(t, 3))
	countAll(memory)
	functionTags := []zone.CounterTag{{Key: "function", Value: "drop"}, {Key: "kind", Value: "function"}}
	before := counterTotal(t, memory, "input", functionTags...)

	err := updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("drop_chain", 3, "b"), chainOf("light", 1, "c")},
	})
	require.NoError(t, err)

	function := publishedFunction(t, memory, "drop")
	require.Equal(t, uint64(2), function.ChainCount)
	chains := function.Chains(2)
	require.Equal(t, "drop_chain", shm.String(chains[0].CpChain.Get().Name[:]))
	require.Equal(t, uint64(3), chains[0].Weight)
	require.Equal(t, "light", shm.String(chains[1].CpChain.Get().Name[:]))

	require.Len(t, handlePackets(t, memory, icmpPackets(t, 5)), 5, "the chains' blackholes drop every packet")
	require.Equal(t, before+5, counterTotal(t, memory, "input", functionTags...), "function counters continue")
	drops := uint64(0)
	for _, name := range []string{"b", "c"} {
		drops += counterTotal(t, memory, "drop",
			zone.CounterTag{Key: "module_name", Value: name}, zone.CounterTag{Key: "kind", Value: "module"},
		)
	}
	require.Equal(t, uint64(5), drops, "the chains' modules count the drops")
	chainTags := []zone.CounterTag{{Key: "chain", Value: "drop_chain"}, {Key: "kind", Value: "chain"}}
	require.Equal(t, uint64(2), counterTotal(t, memory, "pending_input", chainTags...), "the chain's storage keeps its values")
}

// Test_UpdateFunctions_RefusesMissingModule verifies that a function whose
// chain runs an unpublished module is refused by the contexts' build,
// publishing nothing and taking nothing.
func Test_UpdateFunctions_RefusesMissingModule(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState(memory)

	err := updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "drop",
		Chains: []cpconfig.FunctionChainConfig{chainOf("drop_chain", 1, "absent")},
	})

	require.ErrorIs(t, err, cpconfig.ErrMissingModule)
	requireLeftAsFound[*layout.CpModule](t, before, memory)
}

// Test_UpdateFunctions_RefusesRepeatedFunction verifies that an update
// naming one function twice is refused, publishing nothing and taking
// nothing.
func Test_UpdateFunctions_RefusesRepeatedFunction(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState(memory)
	function := cpconfig.FunctionConfig{Name: "twice", Chains: []cpconfig.FunctionChainConfig{chainOf("chain", 1, "a")}}

	err := updateFunctions(t, memory, function, function)

	require.ErrorIs(t, err, cpconfig.ErrRepeated)
	requireLeftAsFound[*layout.CpModule](t, before, memory)
}

// buildPlace matches where a failed build of functions or pipelines
// stopped: the worker whose contexts it built, the step of the function,
// chain or pipeline it built, or the upsert of one.
var buildPlace = regexp.MustCompile(
	`worker \d+|(function|pipeline) "[^"]*": (chain "[^"]*": )?(allocate \w+|register)|` +
		`upsert (function|pipeline) "[^"]*"`,
)

// requireExhaustionLeavesAsFound runs an update on fresh fixtures whose
// zone keeps the blocks of the copy and those of each budget, and fails
// unless it stops at every one of the places.
//
// An update that runs out of memory must leave the shared state as it
// found it. A budget may also carry the update through: the contexts'
// sizes differ between release and sanitizer builds, so the budgets past
// the update's own requests overlap to stop it in each worker of both.
func requireExhaustionLeavesAsFound(
	t *testing.T,
	fixture func(t testing.TB) *publishedFixture,
	update func(t *testing.T, memory *publishedFixture) error,
	budgets []requestBudget,
	places []string,
) {
	t.Helper()
	stopped := map[string]bool{}
	for _, budget := range budgets {
		t.Run(budget.name, func(t *testing.T) {
			memory := fixture(t)
			cp := memory.Instance().Cp
			kept := append(copyRequests(cp.CpConfigGen.Get()), budget.requests...)
			nativetest.LeaveOnly(t, &cp.BlockAllocator, kept...)
			before := sharedState(memory)

			err := update(t, memory)

			if err == nil {
				return
			}
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireLeftAsFound[*layout.CpModule](t, before, memory)
			place := buildPlace.FindString(err.Error())
			require.NotEmpty(t, place, "the update stopped at a known place: %v", err)
			stopped[place] = true
		})
	}
	want := map[string]bool{}
	for _, place := range places {
		want[place] = true
	}
	require.Equal(t, want, stopped)
}

// contextTails are the further requests that carry a build into the
// workers' contexts and stop it in each of them, in release and sanitizer
// builds alike.
var contextTails = [][]uintptr{
	{32 * kilobyte},
	{64 * kilobyte},
	{64 * kilobyte, 4 * kilobyte},
	{64 * kilobyte, 8 * kilobyte},
	{64 * kilobyte, 32 * kilobyte},
	{32 * kilobyte, 32 * kilobyte, 4 * kilobyte},
	{32 * kilobyte, 32 * kilobyte, 8 * kilobyte},
}

// requestBudget is the requests the zone serves beyond the copy's.
type requestBudget struct {
	name     string
	requests []uintptr
}

// prefixBudgets returns every prefix of a build's request sequence, then
// the whole sequence with each tail of further requests.
//
// A prefix serves the steps of the build up to the first request it
// lacks, so the prefixes stop the build at each of its steps; the tails
// carry it into the workers' contexts.
func prefixBudgets(requests []uintptr, tails ...[]uintptr) []requestBudget {
	var budgets []requestBudget
	for end := range len(requests) + 1 {
		budgets = append(budgets, requestBudget{
			name:     fmt.Sprintf("first %d of %d build requests", end, len(requests)),
			requests: requests[:end:end],
		})
	}
	for _, tail := range tails {
		budgets = append(budgets, requestBudget{
			name:     fmt.Sprintf("build requests and %v", tail),
			requests: append(slices.Clip(requests), tail...),
		})
	}
	return budgets
}

// counterRequests returns what registering that many counters in a fresh
// registry asks for: the names, then each growth of their string index,
// which grows on the first, third and fifth name.
func counterRequests(count int) []uintptr {
	requests := []uintptr{8 * unsafe.Sizeof(layout.Counter{})}
	growths := []struct {
		names int
		slots uintptr
	}{{1, 8}, {3, 16}, {5, 32}}
	for _, growth := range growths {
		if count >= growth.names {
			requests = append(requests, growth.slots*4)
		}
	}
	return requests
}

// functionRequests returns what building a function of chains of those
// lengths asks the zone for, in order.
func functionRequests(chainLengths ...int) []uintptr {
	requests := []uintptr{unsafe.Sizeof(layout.CpFunction{}) + uintptr(len(chainLengths))*unsafe.Sizeof(layout.CpFunctionChain{})}
	requests = append(requests, counterRequests(6)...)
	for _, length := range chainLengths {
		requests = append(requests, unsafe.Sizeof(layout.CpChain{})+uintptr(length)*unsafe.Sizeof(layout.CpChainModule{}))
		requests = append(requests, counterRequests(2)...)
	}
	return requests
}

// Test_UpdateFunctions_ExhaustedZoneFreesUpdate verifies that an update of
// functions the zone runs out of memory for at any step frees every block
// it took and drops every reference it took.
//
// The update replaces the published function and adds a new one, so a
// failure inside the second finds the first in the copy. On a full
// registry the upsert of a new function fails to grow it, and the
// function it refused goes back too.
func Test_UpdateFunctions_ExhaustedZoneFreesUpdate(t *testing.T) {
	t.Run("registry with room", func(t *testing.T) {
		requests := append(functionRequests(1), functionRequests(1, 1)...)
		budgets := prefixBudgets(requests, contextTails...)
		requireExhaustionLeavesAsFound(t, newPublishedFixture, func(t *testing.T, memory *publishedFixture) error {
			return updateFunctions(t, memory,
				cpconfig.FunctionConfig{Name: "drop", Chains: []cpconfig.FunctionChainConfig{chainOf("drop_chain", 1, "a")}},
				cpconfig.FunctionConfig{Name: "fresh", Chains: []cpconfig.FunctionChainConfig{
					chainOf("first", 1, "b"), chainOf("second", 1, "c"),
				}},
			)
		}, budgets, []string{
			`function "drop": allocate function`,
			`function "drop": register`,
			`function "drop": chain "drop_chain": allocate chain`,
			`function "drop": chain "drop_chain": register`,
			`function "fresh": allocate function`,
			`function "fresh": register`,
			`function "fresh": chain "first": allocate chain`,
			`function "fresh": chain "first": register`,
			`function "fresh": chain "second": allocate chain`,
			`function "fresh": chain "second": register`,
			"worker 0",
			"worker 1",
		})
	})
	t.Run("full registry", func(t *testing.T) {
		requests := functionRequests(1)
		budgets := []requestBudget{
			{"no room for grown slots", requests},
			{"no room for a grown index", append(slices.Clip(requests), 16*shm.PtrSize)},
		}
		requireExhaustionLeavesAsFound(t, newFullFunctionsFixture, func(t *testing.T, memory *publishedFixture) error {
			return updateFunctions(t, memory, cpconfig.FunctionConfig{
				Name:   "ninth",
				Chains: []cpconfig.FunctionChainConfig{chainOf("chain", 1, "a")},
			})
		}, budgets, []string{`upsert function "ninth"`})
	})
}

// newFullFunctionsFixture returns a published fixture whose function
// registry is full: drop and seven functions no pipeline runs fill its
// eight slots.
func newFullFunctionsFixture(t testing.TB) *publishedFixture {
	t.Helper()
	memory := newPublishedFixture(t)
	var functions []cpconfig.FunctionConfig
	for idx := range 7 {
		functions = append(functions, cpconfig.FunctionConfig{
			Name:   fmt.Sprintf("idle%d", idx),
			Chains: []cpconfig.FunctionChainConfig{chainOf("chain", 1, "a")},
		})
	}
	require.NoError(t, updateFunctions(t, memory, functions...))
	requireFull(t, &memory.Instance().Cp.CpConfigGen.Get().FunctionRegistry.Registry)
	return memory
}

// requireFull fails unless a registry holds eight items in its eight
// slots, so one more grows it.
func requireFull(t testing.TB, items *layout.Registry) {
	t.Helper()
	held := 0
	for range registry.Items(items) {
		held++
	}
	require.Equal(t, uint64(8), items.Capacity)
	require.Equal(t, 8, held)
}

package neighbour_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
)

// Test_Publish_IndependentDeadlines verifies that a blocked replacement cannot
// exhaust the next target's attempt or trigger cleanup of last-good tables.
func Test_Publish_IndependentDeadlines(t *testing.T) {
	service, client := newPublicationService(t)
	first := newPublisherTarget("first", "logical0", client)
	second := newPublisherTarget("second", "logical1", client)
	service.Store("netlink-dataplane-old", publicationTable{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	service.SetHook(func(call publicationCall) error {
		deadline, present := call.Context.Deadline()
		if !present || !deadline.Before(parentDeadline) {
			return errors.New("missing independent deadline")
		}
		if call.Table == first.TableName {
			<-call.Context.Done()
			return status.FromContextError(call.Context.Err()).Err()
		}
		return nil
	})
	err := neighbour.Publish(ctx, []neighbour.Entry{testDesiredEntry("fe80::1", "logical1")}, []neighbour.GatewayTarget{first, second})
	require.ErrorContains(t, err, "DeadlineExceeded")
	require.NoError(t, ctx.Err())
	tables := service.Tables()
	require.NotContains(t, tables, first.TableName)
	require.Len(t, tables[second.TableName].Entries, 1)
	require.Contains(t, tables, "netlink-dataplane-old")
	require.Equal(t, []string{"chunk", "chunk", "commit"}, callMethods(service.Calls()))
	for _, call := range service.Calls() {
		require.Eventually(t, func() bool { return call.Context.Err() != nil }, time.Second, time.Millisecond)
	}
}

// Test_Publish_ReplacementFailureAndRecovery verifies that failed streams keep
// old snapshots and cleanup waits for all independent targets to succeed.
func Test_Publish_ReplacementFailureAndRecovery(t *testing.T) {
	for _, method := range []string{"chunk", "commit"} {
		t.Run(method, func(t *testing.T) {
			service, client := newPublicationService(t)
			first := newPublisherTarget("first", "logical0", client)
			second := newPublisherTarget("second", "logical1", client)
			third := newPublisherTarget("third", "logical2", client)
			service.Store("netlink-dataplane-old", publicationTable{Priority: 7})
			service.Store(second.TableName, publicationTable{Priority: 8})
			service.SetHook(func(call publicationCall) error {
				if call.Method == method && call.Table != third.TableName {
					return status.Error(codes.Unavailable, call.Table+" failed")
				}
				return nil
			})
			targets := []neighbour.GatewayTarget{first, second, third}
			err := neighbour.Publish(t.Context(), nil, targets)
			require.ErrorContains(t, err, first.TableName+" failed")
			require.ErrorContains(t, err, second.TableName+" failed")
			require.Equal(t, uint32(8), service.Tables()[second.TableName].Priority)
			require.Contains(t, service.Tables(), third.TableName)
			require.Contains(t, service.Tables(), "netlink-dataplane-old")
			require.NotContains(t, callMethods(service.Calls()), "list_tables")
			service.SetHook(nil)
			require.NoError(t, neighbour.Publish(t.Context(), nil, targets))
			require.Len(t, service.Tables(), 3)
			require.Equal(t, uint32(100), service.Tables()[second.TableName].Priority)
		})
	}
}

// Test_Publish_CancellationBoundaries verifies that parent cancellation stops
// later chunks, gateway attempts, and obsolete table deletions.
func Test_Publish_CancellationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		table  string
		want   []string
	}{
		{name: "before publication"},
		{name: "during first chunk", method: "chunk", table: "netlink-dataplane-first", want: []string{"chunk"}},
		{name: "at first commit", method: "commit", table: "netlink-dataplane-first", want: []string{"chunk", "commit"}},
		{name: "before shared cleanup", method: "commit", table: "netlink-dataplane-second", want: []string{"chunk", "commit", "chunk", "commit"}},
		{name: "after metadata listing", method: "list_tables", want: []string{"chunk", "commit", "chunk", "commit", "list_tables"}},
		{name: "between stale deletions", method: "remove_table", table: "netlink-dataplane-old-a", want: []string{"chunk", "commit", "chunk", "commit", "list_tables", "remove_table"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, client := newPublicationService(t)
			service.Store("netlink-dataplane-old-a", publicationTable{})
			service.Store("netlink-dataplane-old-z", publicationTable{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service.SetHook(func(call publicationCall) error {
				if call.Method == test.method && call.Table == test.table {
					cancel()
					<-call.Context.Done()
				}
				return nil
			})
			if test.method == "" {
				cancel()
			}
			err := neighbour.Publish(ctx, nil, []neighbour.GatewayTarget{
				newPublisherTarget("first", "logical0", client), newPublisherTarget("second", "logical1", client),
			})
			require.Error(t, err)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.Equal(t, test.want, callMethods(service.Calls()))
			require.Contains(t, service.Tables(), "netlink-dataplane-old-z")
		})
	}
}

// Test_Publish_CleanupFailure verifies that cleanup errors are surfaced without
// undoing complete replacements or deleting later obsolete tables.
func Test_Publish_CleanupFailure(t *testing.T) {
	for _, method := range []string{"list_tables", "remove_table"} {
		t.Run(method, func(t *testing.T) {
			service, client := newPublicationService(t)
			service.Store("netlink-dataplane-old", publicationTable{})
			service.SetHook(func(call publicationCall) error {
				if call.Method == method {
					return status.Error(codes.Unavailable, "cleanup failed")
				}
				return nil
			})
			target := newPublisherTarget("first", "logical0", client)
			require.ErrorContains(t, neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{target}), "cleanup failed")
			require.Contains(t, service.Tables(), target.TableName)
			require.Contains(t, service.Tables(), "netlink-dataplane-old")
		})
	}
}

// Test_Publish_UnsupportedReplacement verifies that a refused streaming API
// remains a failed target, without cleanup or attempts to emulate publication.
func Test_Publish_UnsupportedReplacement(t *testing.T) {
	service, client := newPublicationService(t)
	first := newPublisherTarget("first", "logical0", client)
	second := newPublisherTarget("second", "logical1", client)
	service.Store(first.TableName, publicationTable{Priority: 7})
	service.Store("netlink-dataplane-old", publicationTable{})
	service.SetHook(func(call publicationCall) error {
		if call.Table == first.TableName {
			return status.Error(codes.Unimplemented, "replacement not supported")
		}
		return nil
	})
	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{first, second})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.Equal(t, []string{"chunk", "chunk", "commit"}, callMethods(service.Calls()))
	tables := service.Tables()
	require.Equal(t, uint32(7), tables[first.TableName].Priority)
	require.Equal(t, second.DefaultPriority, tables[second.TableName].Priority)
	require.Contains(t, tables, "netlink-dataplane-old")
}

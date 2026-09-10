package netlink

import (
	"context"
	"errors"
	"time"

	vnetlink "github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// Handle adds cancellable neighbour iteration to the shared kernel handle.
//
// All calls run in the process's private network namespace. A dump uses its own
// socket so cancellation can interrupt reception without damaging mutation I/O.
type Handle struct {
	*vnetlink.Handle
	timeout time.Duration
}

// NewHandle opens the route socket and transfers ownership only on success.
func NewHandle() (*Handle, error) {
	handle, err := vnetlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	return &Handle{Handle: handle, timeout: 5 * time.Second}, nil
}

// SetSocketTimeout bounds shared requests and subsequent neighbour dumps.
//
// Configuration is completed before starting the reconcile workers.
func (m *Handle) SetSocketTimeout(timeout time.Duration) error {
	if err := m.Handle.SetSocketTimeout(timeout); err != nil {
		return err
	}
	m.timeout = timeout
	return nil
}

// WalkNeighbours visits records without retaining the complete kernel dump.
//
// A visitor error or cancellation closes the disposable socket immediately;
// interrupted dumps remain failures even when some records were delivered.
func (m *Handle) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	socket, err := nl.GetNetlinkSocketAt(netns.None(), netns.None(), unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	defer socket.Close()
	stop := context.AfterFunc(ctx, socket.Close)
	defer stop()
	timeout := unix.NsecToTimeval(m.timeout.Nanoseconds())
	if err := socket.SetSendTimeout(&timeout); err != nil {
		return err
	}
	if err := socket.SetReceiveTimeout(&timeout); err != nil {
		return err
	}
	request := nl.NewNetlinkRequest(unix.RTM_GETNEIGH, unix.NLM_F_DUMP)
	request.Sockets = map[int]*nl.SocketHandle{unix.NETLINK_ROUTE: {Socket: socket}}
	message := &vnetlink.Ndmsg{Family: unix.AF_UNSPEC}
	request.AddData(message)
	var visitErr error
	err = request.ExecuteIter(unix.NETLINK_ROUTE, unix.RTM_NEWNEIGH, func(data []byte) bool {
		visitErr = ctx.Err()
		if visitErr == nil {
			if len(data) < message.Len() {
				visitErr = errors.New("truncated neighbour message")
			} else {
				var entry *vnetlink.Neigh
				entry, visitErr = vnetlink.NeighDeserialize(data)
				if visitErr == nil {
					visitErr = visit(*entry)
				}
			}
		}
		if visitErr != nil {
			socket.Close()
			return false
		}
		return true
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(visitErr, err)
}

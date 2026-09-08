package netlink

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// ProcSysctl writes per-interface IPv6 settings below procfs.
type ProcSysctl struct {
	Root string
}

// NewProcSysctl creates a writer rooted at the host IPv6 configuration tree.
func NewProcSysctl() *ProcSysctl {
	return &ProcSysctl{Root: "/proc/sys/net/ipv6/conf"}
}

// SetIPv6 opens one per-interface IPv6 setting, revalidates the link identity,
// and writes through the descriptor bound to that interface instance.
func (m *ProcSysctl) SetIPv6(
	ctx context.Context,
	interfaceName string,
	setting string,
	value string,
	validate func() error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := netplan.ValidateInterfaceName(interfaceName); err != nil {
		return fmt.Errorf("invalid interface name %q: %w", interfaceName, err)
	}
	if setting != "accept_ra" && setting != "addr_gen_mode" {
		return fmt.Errorf("unsupported IPv6 sysctl %q", setting)
	}
	if value != "0" && value != "1" && value != "2" {
		return fmt.Errorf("invalid IPv6 sysctl value %q", value)
	}
	file, err := os.OpenFile(filepath.Join(m.Root, interfaceName, setting), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open IPv6 sysctl %q for interface %q: %w", setting, interfaceName, err)
	}
	defer file.Close()
	if validate == nil {
		return errors.New("link identity validator is nil")
	}
	if err := validate(); err != nil {
		return fmt.Errorf("revalidate interface %q: %w", interfaceName, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := file.WriteString(value); err != nil {
		return fmt.Errorf("write IPv6 sysctl %q for interface %q: %w", setting, interfaceName, err)
	}
	return nil
}

var _ Sysctl = (*ProcSysctl)(nil)

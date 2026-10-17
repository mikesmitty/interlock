//go:build linux

package socketcan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"

	"github.com/mikesmitty/interlock/openlcb/transport"
)

// New returns a link over the named CAN interface, such as can0. The
// interface is opened when the link runs and reopened with backoff when
// it goes away.
func New(name, iface string, logger *slog.Logger) (transport.Link, error) {
	if iface == "" {
		return nil, errors.New("socketcan: an interface name is needed, such as can0")
	}
	return transport.NewFramed(name, &dialer{iface: iface}, &Codec{}, logger), nil
}

// dialer opens a raw CAN socket bound to the interface.
type dialer struct{ iface string }

func (d *dialer) Dial(context.Context) (io.ReadWriteCloser, error) {
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.CAN_RAW)
	if err != nil {
		return nil, fmt.Errorf("socketcan: socket: %w", err)
	}
	ifr, err := unix.NewIfreq(d.iface)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("socketcan: %w", err)
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFINDEX, ifr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("socketcan: interface %s: %w", d.iface, err)
	}
	if err := unix.Bind(fd, &unix.SockaddrCAN{Ifindex: int(ifr.Uint32())}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("socketcan: bind %s: %w", d.iface, err)
	}
	return os.NewFile(uintptr(fd), "socketcan:"+d.iface), nil
}

func (d *dialer) Kind() string   { return Kind }
func (d *dialer) Target() string { return d.iface }

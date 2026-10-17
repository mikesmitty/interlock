//go:build !linux

package socketcan

import (
	"log/slog"

	"github.com/mikesmitty/interlock/openlcb/transport"
)

// New returns ErrUnsupported: SocketCAN is a Linux interface.
func New(string, string, *slog.Logger) (transport.Link, error) {
	return nil, ErrUnsupported
}

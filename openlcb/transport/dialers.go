package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"go.bug.st/serial"
)

// SerialDialer opens a serial port. USB CDC devices such as canbus-outpost
// and simple-dcc ignore the baud rate; real UART adapters need the right
// one.
type SerialDialer struct {
	Device string
	Baud   int
}

// Dial implements Dialer.
func (d SerialDialer) Dial(context.Context) (io.ReadWriteCloser, error) {
	baud := d.Baud
	if baud == 0 {
		baud = 115200
	}
	port, err := serial.Open(d.Device, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", d.Device, err)
	}
	// A USB device that has just enumerated can need a moment before it
	// echoes anything; nothing else to do here.
	return port, nil
}

// Kind implements Dialer.
func (d SerialDialer) Kind() string { return "serial" }

// Target implements Dialer.
func (d SerialDialer) Target() string { return d.Device }

// TCPDialer connects to a GridConnect TCP server such as JMRI's or another
// Interlock's hub port.
type TCPDialer struct {
	Address string
	Timeout time.Duration
}

// Dial implements Dialer.
func (d TCPDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout, KeepAlive: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", d.Address)
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	return conn, nil
}

// Kind implements Dialer.
func (d TCPDialer) Kind() string { return "tcp" }

// Target implements Dialer.
func (d TCPDialer) Target() string { return d.Address }

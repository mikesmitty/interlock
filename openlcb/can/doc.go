// Package can implements the OpenLCB CAN frame transfer layer: the 29-bit
// identifier layout, control frames for alias allocation, GridConnect text
// framing, the alias generator and cache, and the assembly of messages and
// datagrams from frames and back.
//
// It has no goroutines and no I/O; the node and transport packages drive it.
package can

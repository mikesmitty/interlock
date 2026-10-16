package openlcb

import "fmt"

// ErrorCode is an OpenLCB error code as carried in Datagram Rejected,
// Optional Interaction Rejected and Terminate Due To Error messages. Codes
// 0x1xxx are permanent, 0x2xxx temporary (the request may be retried).
type ErrorCode uint16

// Error codes from the Message Network and Datagram standards.
const (
	ErrCodePermanent                 ErrorCode = 0x1000
	ErrCodeSourceNotPermitted        ErrorCode = 0x1020
	ErrCodeNotImplemented            ErrorCode = 0x1040
	ErrCodeNotImplementedSubcommand  ErrorCode = 0x1041
	ErrCodeNotImplementedDatagram    ErrorCode = 0x1042
	ErrCodeNotImplementedMTI         ErrorCode = 0x1043
	ErrCodeInvalidArguments          ErrorCode = 0x1080
	ErrCodeAddressSpaceUnknown       ErrorCode = 0x1081
	ErrCodeAddressOutOfBounds        ErrorCode = 0x1082
	ErrCodeWriteToReadOnly           ErrorCode = 0x1083
	ErrCodeFirmwareIncompatible      ErrorCode = 0x1088
	ErrCodeFirmwareCorrupted         ErrorCode = 0x1089
	ErrCodeTemporary                 ErrorCode = 0x2000
	ErrCodeTimeout                   ErrorCode = 0x2010
	ErrCodeBufferUnavailable         ErrorCode = 0x2020
	ErrCodeTransferError             ErrorCode = 0x2030
	ErrCodeTransferTimeout           ErrorCode = 0x2031
	ErrCodeOutOfOrder                ErrorCode = 0x2040
	ErrCodeOutOfOrderMiddleWithStart ErrorCode = 0x2041
	ErrCodeOutOfOrderStartBeforeEnd  ErrorCode = 0x2042
	ErrCodeFirmwareChecksum          ErrorCode = 0x2088
)

// Temporary reports whether the request may be retried.
func (e ErrorCode) Temporary() bool { return e&0xF000 == 0x2000 }

// Permanent reports whether retrying cannot help.
func (e ErrorCode) Permanent() bool { return e&0xF000 == 0x1000 }

var errorCodeNames = map[ErrorCode]string{
	ErrCodePermanent:                 "permanent error",
	ErrCodeSourceNotPermitted:        "source not permitted",
	ErrCodeNotImplemented:            "not implemented",
	ErrCodeNotImplementedSubcommand:  "subcommand not implemented",
	ErrCodeNotImplementedDatagram:    "datagram type not implemented",
	ErrCodeNotImplementedMTI:         "MTI not implemented",
	ErrCodeInvalidArguments:          "invalid arguments",
	ErrCodeAddressSpaceUnknown:       "address space unknown",
	ErrCodeAddressOutOfBounds:        "address out of bounds",
	ErrCodeWriteToReadOnly:           "write to read-only space",
	ErrCodeFirmwareIncompatible:      "firmware incompatible with this hardware",
	ErrCodeFirmwareCorrupted:         "firmware image corrupted",
	ErrCodeTemporary:                 "temporary error",
	ErrCodeTimeout:                   "timeout",
	ErrCodeBufferUnavailable:         "buffer unavailable",
	ErrCodeTransferError:             "transfer error",
	ErrCodeTransferTimeout:           "transfer timed out",
	ErrCodeOutOfOrder:                "out of order",
	ErrCodeOutOfOrderMiddleWithStart: "middle or end frame without start",
	ErrCodeOutOfOrderStartBeforeEnd:  "start frame before previous end",
	ErrCodeFirmwareChecksum:          "firmware checksum failed, retry",
}

// String names the code, falling back to its class and hex value.
func (e ErrorCode) String() string {
	if name, ok := errorCodeNames[e]; ok {
		return fmt.Sprintf("%s (0x%04X)", name, uint16(e))
	}
	class := "error"
	switch {
	case e.Temporary():
		class = "temporary error"
	case e.Permanent():
		class = "permanent error"
	}
	return fmt.Sprintf("%s 0x%04X", class, uint16(e))
}

// Error is an error carrying an OpenLCB error code from a remote node.
type Error struct {
	Code ErrorCode
	// Op says what was being done, for example "datagram to 05.01.01.01.22.00".
	Op string
}

func (e *Error) Error() string {
	if e.Op == "" {
		return e.Code.String()
	}
	return e.Op + ": " + e.Code.String()
}

// Temporary reports whether the operation may be retried.
func (e *Error) Temporary() bool { return e.Code.Temporary() }

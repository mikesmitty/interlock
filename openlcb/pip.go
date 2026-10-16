package openlcb

import "strings"

// Protocols is the Protocol Identification Protocol bitmap: which optional
// protocols a node supports. On the wire it is six (or more) big-endian
// bytes; here it is held in the upper 48 bits of a uint64 shifted down so
// the first wire byte is bits 47 to 40.
type Protocols uint64

// Protocol bits.
const (
	ProtocolSimple                Protocols = 0x800000000000
	ProtocolDatagram              Protocols = 0x400000000000
	ProtocolStream                Protocols = 0x200000000000
	ProtocolMemoryConfiguration   Protocols = 0x100000000000
	ProtocolReservation           Protocols = 0x080000000000
	ProtocolEventExchange         Protocols = 0x040000000000
	ProtocolIdentification        Protocols = 0x020000000000
	ProtocolTeachingLearning      Protocols = 0x010000000000
	ProtocolRemoteButton          Protocols = 0x008000000000
	ProtocolAbbreviatedDefaultCDI Protocols = 0x004000000000
	ProtocolDisplay               Protocols = 0x002000000000
	ProtocolSimpleNodeInformation Protocols = 0x001000000000
	ProtocolCDI                   Protocols = 0x000800000000
	ProtocolTractionControl       Protocols = 0x000400000000
	ProtocolFunctionDescription   Protocols = 0x000200000000
	ProtocolDCCCommandStation     Protocols = 0x000100000000
	ProtocolSimpleTrainNode       Protocols = 0x000080000000
	ProtocolFunctionConfiguration Protocols = 0x000040000000
	ProtocolFirmwareUpgrade       Protocols = 0x000020000000
	ProtocolFirmwareUpgradeActive Protocols = 0x000010000000
)

var protocolNames = []struct {
	bit  Protocols
	name string
}{
	{ProtocolSimple, "Simple"},
	{ProtocolDatagram, "Datagram"},
	{ProtocolStream, "Stream"},
	{ProtocolMemoryConfiguration, "Memory Configuration"},
	{ProtocolReservation, "Reservation"},
	{ProtocolEventExchange, "Event Exchange"},
	{ProtocolIdentification, "Identification"},
	{ProtocolTeachingLearning, "Teaching/Learning"},
	{ProtocolRemoteButton, "Remote Button"},
	{ProtocolAbbreviatedDefaultCDI, "Abbreviated Default CDI"},
	{ProtocolDisplay, "Display"},
	{ProtocolSimpleNodeInformation, "Simple Node Information"},
	{ProtocolCDI, "CDI"},
	{ProtocolTractionControl, "Traction Control"},
	{ProtocolFunctionDescription, "Function Description"},
	{ProtocolDCCCommandStation, "DCC Command Station"},
	{ProtocolSimpleTrainNode, "Simple Train Node"},
	{ProtocolFunctionConfiguration, "Function Configuration"},
	{ProtocolFirmwareUpgrade, "Firmware Upgrade"},
	{ProtocolFirmwareUpgradeActive, "Firmware Upgrade Active"},
}

// Has reports whether every bit in flags is set.
func (p Protocols) Has(flags Protocols) bool { return p&flags == flags }

// Names lists the supported protocols in wire order.
func (p Protocols) Names() []string {
	var names []string
	for _, entry := range protocolNames {
		if p&entry.bit != 0 {
			names = append(names, entry.name)
		}
	}
	return names
}

// String joins the names with commas.
func (p Protocols) String() string {
	names := p.Names()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// Bytes returns the six-byte wire form.
func (p Protocols) Bytes() []byte {
	return []byte{
		byte(p >> 40), byte(p >> 32), byte(p >> 24),
		byte(p >> 16), byte(p >> 8), byte(p),
	}
}

// ParseProtocols reads a Protocol Support Reply payload. Replies shorter
// than six bytes are padded with zeros; bytes past the sixth are ignored.
func ParseProtocols(data []byte) Protocols {
	var p Protocols
	for i := 0; i < 6; i++ {
		p <<= 8
		if i < len(data) {
			p |= Protocols(data[i])
		}
	}
	return p
}

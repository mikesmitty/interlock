package monitor

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/clock"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
)

// Lookup supplies the node ID behind an alias, or 0.
type Lookup func(openlcb.Alias) openlcb.NodeID

func endpoint(alias openlcb.Alias, id openlcb.NodeID, lookup Lookup) *Endpoint {
	if alias == 0 && id == 0 {
		return nil
	}
	if id == 0 && lookup != nil {
		id = lookup(alias)
	}
	return &Endpoint{Alias: uint16(alias), ID: id}
}

func hexString(b []byte) string {
	return strings.ToUpper(hex.EncodeToString(b))
}

// DecodeFrame explains one CAN frame. It returns the entry kind and the
// decoded view, which is nil for frames that are not OpenLCB.
func DecodeFrame(f can.Frame, lookup Lookup) (string, *Decoded) {
	if !f.Extended {
		return "other", nil
	}
	if f.IsControl() {
		return "control", decodeControl(f, lookup)
	}
	switch f.Type() {
	case can.FrameTypeGlobalAddressed:
		return decodeMessageFrame(f, lookup)
	case can.FrameTypeDatagramOnly, can.FrameTypeDatagramFirst, can.FrameTypeDatagramMiddle, can.FrameTypeDatagramLast:
		return decodeDatagramFrame(f, lookup)
	case can.FrameTypeStreamData:
		return "other", &Decoded{Type: "Stream Data", Source: endpoint(f.SourceAlias(), 0, lookup), Dest: endpoint(f.DatagramDest(), 0, lookup), Payload: hexString(f.Payload())}
	default:
		return "other", &Decoded{Type: fmt.Sprintf("Reserved frame type %d", f.Type()), Source: endpoint(f.SourceAlias(), 0, lookup)}
	}
}

func decodeControl(f can.Frame, lookup Lookup) *Decoded {
	d := &Decoded{Source: endpoint(f.SourceAlias(), 0, lookup)}
	if f.IsCID() {
		n := 8 - int(f.ControlSequence())
		d.Type = fmt.Sprintf("CID%d", n)
		d.Summary = fmt.Sprintf("Check ID %d of 4: node ID bits %03X", n, f.ControlField()&0x0FFF)
		return d
	}
	id, hasID := f.PayloadNodeID()
	switch f.ControlField() {
	case can.ControlRID:
		d.Type = "RID"
		d.Summary = "Reserve ID: alias claimed"
	case can.ControlAMD:
		d.Type = "AMD"
		d.Source.ID = id
		d.Summary = "Alias Map Definition: alias is " + id.String()
	case can.ControlAME:
		d.Type = "AME"
		if hasID {
			d.Summary = "Alias Map Enquiry: who is " + id.String() + "?"
		} else {
			d.Summary = "Alias Map Enquiry: everyone announce your alias"
		}
	case can.ControlAMR:
		d.Type = "AMR"
		d.Source.ID = id
		d.Summary = "Alias Map Reset: " + id.String() + " gave up its alias"
	default:
		d.Type = fmt.Sprintf("Control 0x%04X", f.ControlField())
	}
	return d
}

func decodeMessageFrame(f can.Frame, lookup Lookup) (string, *Decoded) {
	mti := f.MTI()
	d := &Decoded{Type: mti.String(), MTI: uint16(mti), Source: endpoint(f.SourceAlias(), 0, lookup)}
	payload := f.Payload()
	if mti.IsAddressed() {
		dst, ok := f.AddressedDest()
		if !ok {
			d.Summary = "addressed frame without destination"
			return "message", d
		}
		d.Dest = endpoint(dst, 0, lookup)
		flags := f.Data[0] & 0x30
		payload = payload[2:]
		d.Payload = hexString(payload)
		if flags != 0 {
			part := map[byte]string{0x10: "first", 0x20: "last", 0x30: "middle"}[flags]
			d.Summary = part + " frame of a longer message"
			return "fragment", d
		}
	} else {
		d.Payload = hexString(payload)
	}
	msg := &openlcb.Message{MTI: mti, Source: openlcb.NodeHandle{Alias: f.SourceAlias()}, Payload: payload}
	if d.Dest != nil {
		msg.Dest = openlcb.NodeHandle{Alias: openlcb.Alias(d.Dest.Alias)}
	}
	d.Summary = summarize(msg)
	return "message", d
}

func decodeDatagramFrame(f can.Frame, lookup Lookup) (string, *Decoded) {
	d := &Decoded{Type: "Datagram", MTI: uint16(openlcb.MTIDatagram), Source: endpoint(f.SourceAlias(), 0, lookup), Dest: endpoint(f.DatagramDest(), 0, lookup), Payload: hexString(f.Payload())}
	switch f.Type() {
	case can.FrameTypeDatagramOnly:
		d.Summary = summarizeDatagram(f.Payload())
		return "datagram", d
	case can.FrameTypeDatagramFirst:
		d.Summary = "first frame: " + summarizeDatagram(f.Payload())
	case can.FrameTypeDatagramMiddle:
		d.Summary = "middle frame"
	default:
		d.Summary = "last frame"
	}
	return "fragment", d
}

// DecodeMessage explains an assembled message.
func DecodeMessage(msg *openlcb.Message) *Decoded {
	d := &Decoded{
		Type:    msg.MTI.String(),
		MTI:     uint16(msg.MTI),
		Source:  endpoint(msg.Source.Alias, msg.Source.ID, nil),
		Dest:    endpoint(msg.Dest.Alias, msg.Dest.ID, nil),
		Payload: hexString(msg.Payload),
		Summary: summarize(msg),
	}
	return d
}

// summarize explains a message's payload in a sentence.
func summarize(msg *openlcb.Message) string {
	p := msg.Payload
	switch msg.MTI {
	case openlcb.MTIInitializationComplete, openlcb.MTIInitializationCompleteSimple:
		if id, ok := msg.PayloadNodeID(); ok {
			return id.String() + " is up"
		}
	case openlcb.MTIVerifiedNodeID, openlcb.MTIVerifiedNodeIDSimple:
		if id, ok := msg.PayloadNodeID(); ok {
			return "I am " + id.String()
		}
	case openlcb.MTIVerifyNodeIDGlobal:
		if id, ok := msg.PayloadNodeID(); ok {
			return "is " + id.String() + " here?"
		}
		return "everyone identify yourselves"
	case openlcb.MTIVerifyNodeIDAddressed:
		return "who are you?"
	case openlcb.MTIProtocolSupportInquiry:
		return "which protocols do you support?"
	case openlcb.MTIProtocolSupportReply:
		return openlcb.ParseProtocols(p).String()
	case openlcb.MTISimpleNodeIdentInfoRequest:
		return "identify yourself"
	case openlcb.MTISimpleNodeIdentInfoReply:
		if snip, err := openlcb.ParseSNIP(p); err == nil {
			parts := []string{snip.Manufacturer, snip.Model}
			if snip.UserName != "" {
				parts = append(parts, "\""+snip.UserName+"\"")
			}
			return strings.TrimSpace(strings.Join(parts, " "))
		}
	case openlcb.MTIEventReport, openlcb.MTIIdentifyConsumer, openlcb.MTIIdentifyProducer,
		openlcb.MTIConsumerIdentifiedValid, openlcb.MTIConsumerIdentifiedInvalid, openlcb.MTIConsumerIdentifiedUnknown,
		openlcb.MTIProducerIdentifiedValid, openlcb.MTIProducerIdentifiedInvalid, openlcb.MTIProducerIdentifiedUnknown,
		openlcb.MTIConsumerRangeIdentified, openlcb.MTIProducerRangeIdentified, openlcb.MTILearnEvent:
		if id, ok := msg.EventID(); ok {
			s := id.String()
			if name := openlcb.WellKnownName(id); name != "" {
				s += " (" + name + ")"
			} else if name := clockName(id); name != "" {
				s += " (" + name + ")"
			}
			return s
		}
	case openlcb.MTIIdentifyEventsGlobal:
		return "everyone list your events"
	case openlcb.MTIIdentifyEventsAddressed:
		return "list your events"
	case openlcb.MTIDatagram:
		return summarizeDatagram(p)
	case openlcb.MTIDatagramOK:
		if len(p) > 0 && p[0]&0x80 != 0 {
			return fmt.Sprintf("received, reply pending (timeout 2^%d s)", p[0]&0x0F)
		}
		return "received"
	case openlcb.MTIDatagramRejected, openlcb.MTIOptionalInteractionRejected, openlcb.MTITerminateDueToError:
		if len(p) >= 2 {
			code := openlcb.ErrorCode(uint16(p[0])<<8 | uint16(p[1]))
			s := code.String()
			if len(p) >= 4 && msg.MTI != openlcb.MTIDatagramRejected {
				s += " for " + openlcb.MTI(uint16(p[2])<<8|uint16(p[3])).String()
			}
			return s
		}
	case openlcb.MTITractionControlCommand:
		return summarizeTraction(p, false)
	case openlcb.MTITractionControlReply:
		return summarizeTraction(p, true)
	}
	return ""
}

// Memory configuration datagram commands.
const (
	memWrite           = 0x00
	memWriteReplyOK    = 0x10
	memWriteReplyFail  = 0x18
	memRead            = 0x40
	memReadReplyOK     = 0x50
	memReadReplyFail   = 0x58
	memGetOptions      = 0x80
	memOptionsReply    = 0x82
	memGetSpaceInfo    = 0x84
	memSpaceInfoReply  = 0x86
	memLock            = 0x88
	memLockReply       = 0x8A
	memGetUniqueID     = 0x8C
	memUniqueIDReply   = 0x8D
	memUnfreeze        = 0xA0
	memFreeze          = 0xA1
	memUpdateComplete  = 0xA8
	memReset           = 0xA9
	memFactoryReset    = 0xAA
	memContentTypeMask = 0xFC
)

func summarizeDatagram(p []byte) string {
	if len(p) == 0 {
		return "empty datagram"
	}
	switch p[0] {
	case 0x20:
		return summarizeMemConfig(p[1:])
	case 0x01:
		return "stream datagram"
	case 0x30:
		return "train control datagram"
	default:
		return fmt.Sprintf("datagram type 0x%02X", p[0])
	}
}

func memSpaceAndAddress(cmd byte, p []byte) (space uint8, addr uint32, rest []byte, ok bool) {
	if len(p) < 5 {
		return 0, 0, nil, false
	}
	addr = uint32(p[1])<<24 | uint32(p[2])<<16 | uint32(p[3])<<8 | uint32(p[4])
	rest = p[5:]
	switch cmd & 0x03 {
	case 0:
		if len(rest) < 1 {
			return 0, 0, nil, false
		}
		space = rest[0]
		rest = rest[1:]
	case 1:
		space = 0xFD
	case 2:
		space = 0xFE
	case 3:
		space = 0xFF
	}
	return space, addr, rest, true
}

func summarizeMemConfig(p []byte) string {
	if len(p) == 0 {
		return "memory configuration (empty)"
	}
	cmd := p[0]
	switch cmd & memContentTypeMask {
	case memWrite:
		space, addr, data, ok := memSpaceAndAddress(cmd, p)
		if ok {
			return fmt.Sprintf("write %d bytes to %s at 0x%X", len(data), memconfig.SpaceName(space), addr)
		}
	case memWriteReplyOK:
		space, addr, _, ok := memSpaceAndAddress(cmd, p)
		if ok {
			return fmt.Sprintf("write to %s at 0x%X done", memconfig.SpaceName(space), addr)
		}
	case memWriteReplyFail:
		space, addr, rest, ok := memSpaceAndAddress(cmd, p)
		if ok && len(rest) >= 2 {
			return fmt.Sprintf("write to %s at 0x%X failed: %v", memconfig.SpaceName(space), addr, openlcb.ErrorCode(uint16(rest[0])<<8|uint16(rest[1])))
		}
	case memRead:
		space, addr, rest, ok := memSpaceAndAddress(cmd, p)
		if ok && len(rest) >= 1 {
			return fmt.Sprintf("read %d bytes from %s at 0x%X", rest[0], memconfig.SpaceName(space), addr)
		}
	case memReadReplyOK:
		space, addr, data, ok := memSpaceAndAddress(cmd, p)
		if ok {
			return fmt.Sprintf("%d bytes from %s at 0x%X", len(data), memconfig.SpaceName(space), addr)
		}
	case memReadReplyFail:
		space, addr, rest, ok := memSpaceAndAddress(cmd, p)
		if ok && len(rest) >= 2 {
			return fmt.Sprintf("read from %s at 0x%X failed: %v", memconfig.SpaceName(space), addr, openlcb.ErrorCode(uint16(rest[0])<<8|uint16(rest[1])))
		}
	}
	switch cmd {
	case memGetOptions:
		return "get configuration options"
	case memOptionsReply:
		return "configuration options"
	case memGetSpaceInfo:
		if len(p) >= 2 {
			return "get information about " + memconfig.SpaceName(p[1])
		}
	case memSpaceInfoReply, memSpaceInfoReply + 1:
		if len(p) >= 2 {
			present := "not present"
			if cmd&1 != 0 {
				present = "present"
			}
			return memconfig.SpaceName(p[1]) + " is " + present
		}
	case memLock:
		return "lock or reserve node"
	case memLockReply:
		return "lock reply"
	case memGetUniqueID:
		return "get unique ID"
	case memUniqueIDReply:
		return "unique ID reply"
	case memUnfreeze:
		return "unfreeze (end firmware upgrade)"
	case memFreeze:
		return "freeze (begin firmware upgrade)"
	case memUpdateComplete:
		return "configuration update complete"
	case memReset:
		return "reboot"
	case memFactoryReset:
		return "factory reset"
	}
	return fmt.Sprintf("memory configuration command 0x%02X", cmd)
}

func summarizeTraction(p []byte, reply bool) string {
	if len(p) == 0 {
		return ""
	}
	cmd := p[0] & 0x7F
	names := map[byte]string{
		0x00: "set speed and direction", 0x01: "set function", 0x02: "emergency stop",
		0x10: "query speeds", 0x11: "query function", 0x20: "controller configuration",
		0x30: "listener configuration", 0x40: "traction management",
	}
	name, ok := names[cmd]
	if !ok {
		name = fmt.Sprintf("subcommand 0x%02X", cmd)
	}
	if len(p) >= 2 && (cmd == 0x20 || cmd == 0x30 || cmd == 0x40) {
		sub := map[byte]map[byte]string{
			0x20: {0x01: "assign controller", 0x02: "release controller", 0x03: "query controller", 0x04: "controller changing"},
			0x30: {0x01: "attach listener", 0x02: "detach listener", 0x03: "query listeners"},
			0x40: {0x01: "reserve", 0x02: "release", 0x03: "heartbeat"},
		}
		if s, ok := sub[cmd][p[1]]; ok {
			name = s
		}
	}
	if reply {
		name += " reply"
	}
	if p[0]&0x80 != 0 {
		name += " (forwarded to listener)"
	}
	return name
}

// clockName describes a Broadcast Time event of one of the well-known
// clocks, such as "fast clock reports time 06:42".
func clockName(id openlcb.EventID) string {
	var which string
	switch id.Node() {
	case clock.DefaultFastClock:
		which = "fast clock"
	case clock.DefaultRealTimeClock:
		which = "real time clock"
	case clock.AlternateClock1:
		which = "alternate clock 1"
	case clock.AlternateClock2:
		which = "alternate clock 2"
	default:
		return ""
	}
	ev, ok := clock.Parse(id)
	if !ok {
		return which
	}
	return which + " " + strings.TrimPrefix(ev.String(), "clock ")
}

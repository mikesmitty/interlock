package openlcb

import "fmt"

// MTI is a Message Type Indicator from the Message Network standard: the
// 12 significant bits that name a message, plus the flag bits that say
// whether it is addressed, carries an event ID and so on.
type MTI uint16

// Message type indicators.
const (
	MTIInitializationComplete       MTI = 0x0100
	MTIInitializationCompleteSimple MTI = 0x0101
	MTIVerifyNodeIDAddressed        MTI = 0x0488
	MTIVerifyNodeIDGlobal           MTI = 0x0490
	MTIVerifiedNodeID               MTI = 0x0170
	MTIVerifiedNodeIDSimple         MTI = 0x0171
	MTIOptionalInteractionRejected  MTI = 0x0068
	MTITerminateDueToError          MTI = 0x00A8
	MTIProtocolSupportInquiry       MTI = 0x0828
	MTIProtocolSupportReply         MTI = 0x0668
	MTIIdentifyConsumer             MTI = 0x08F4
	MTIConsumerRangeIdentified      MTI = 0x04A4
	MTIConsumerIdentifiedUnknown    MTI = 0x04C7
	MTIConsumerIdentifiedValid      MTI = 0x04C4
	MTIConsumerIdentifiedInvalid    MTI = 0x04C5
	MTIConsumerIdentifiedReserved   MTI = 0x04C6
	MTIIdentifyProducer             MTI = 0x0914
	MTIProducerRangeIdentified      MTI = 0x0524
	MTIProducerIdentifiedUnknown    MTI = 0x0547
	MTIProducerIdentifiedValid      MTI = 0x0544
	MTIProducerIdentifiedInvalid    MTI = 0x0545
	MTIProducerIdentifiedReserved   MTI = 0x0546
	MTIIdentifyEventsAddressed      MTI = 0x0968
	MTIIdentifyEventsGlobal         MTI = 0x0970
	MTILearnEvent                   MTI = 0x0594
	MTIEventReport                  MTI = 0x05B4
	MTITractionControlCommand       MTI = 0x05EB
	MTITractionControlReply         MTI = 0x01E9
	MTISimpleNodeIdentInfoRequest   MTI = 0x0DE8
	MTISimpleNodeIdentInfoReply     MTI = 0x0A08
	MTIDatagram                     MTI = 0x1C48
	MTIDatagramOK                   MTI = 0x0A28
	MTIDatagramRejected             MTI = 0x0A48
	MTIStreamInitiateRequest        MTI = 0x0CC8
	MTIStreamInitiateReply          MTI = 0x0868
	MTIStreamData                   MTI = 0x1F88
	MTIStreamDataProceed            MTI = 0x0888
	MTIStreamDataComplete           MTI = 0x08A8
)

// MTI flag bits.
const (
	mtiModifierMask  MTI = 0x0003
	mtiEventMask     MTI = 0x0004
	mtiAddressedMask MTI = 0x0008
	mtiSimpleMask    MTI = 0x0010
	mtiPriorityMask  MTI = 0x0C00
	mtiSpecialMask   MTI = 0x1000
)

// IsAddressed reports whether messages with this MTI carry a destination.
func (m MTI) IsAddressed() bool { return m&mtiAddressedMask != 0 }

// HasEvent reports whether the payload begins with an event ID.
func (m MTI) HasEvent() bool { return m&mtiEventMask != 0 }

// IsSimple reports whether the MTI belongs to the simple protocol subset.
func (m MTI) IsSimple() bool { return m&mtiSimpleMask != 0 }

// IsSpecial reports whether the MTI is a datagram or stream type, which
// travel in their own CAN frame formats rather than as plain messages.
func (m MTI) IsSpecial() bool { return m&mtiSpecialMask != 0 }

// Priority returns the message priority, 0 (highest) to 3.
func (m MTI) Priority() uint8 { return uint8((m & mtiPriorityMask) >> 10) }

var mtiNames = map[MTI]string{
	MTIInitializationComplete:       "Initialization Complete",
	MTIInitializationCompleteSimple: "Initialization Complete (simple)",
	MTIVerifyNodeIDAddressed:        "Verify Node ID",
	MTIVerifyNodeIDGlobal:           "Verify Node ID Global",
	MTIVerifiedNodeID:               "Verified Node ID",
	MTIVerifiedNodeIDSimple:         "Verified Node ID (simple)",
	MTIOptionalInteractionRejected:  "Optional Interaction Rejected",
	MTITerminateDueToError:          "Terminate Due To Error",
	MTIProtocolSupportInquiry:       "Protocol Support Inquiry",
	MTIProtocolSupportReply:         "Protocol Support Reply",
	MTIIdentifyConsumer:             "Identify Consumer",
	MTIConsumerRangeIdentified:      "Consumer Range Identified",
	MTIConsumerIdentifiedUnknown:    "Consumer Identified (unknown)",
	MTIConsumerIdentifiedValid:      "Consumer Identified (valid)",
	MTIConsumerIdentifiedInvalid:    "Consumer Identified (invalid)",
	MTIConsumerIdentifiedReserved:   "Consumer Identified (reserved)",
	MTIIdentifyProducer:             "Identify Producer",
	MTIProducerRangeIdentified:      "Producer Range Identified",
	MTIProducerIdentifiedUnknown:    "Producer Identified (unknown)",
	MTIProducerIdentifiedValid:      "Producer Identified (valid)",
	MTIProducerIdentifiedInvalid:    "Producer Identified (invalid)",
	MTIProducerIdentifiedReserved:   "Producer Identified (reserved)",
	MTIIdentifyEventsAddressed:      "Identify Events",
	MTIIdentifyEventsGlobal:         "Identify Events Global",
	MTILearnEvent:                   "Learn Event",
	MTIEventReport:                  "Event Report",
	MTITractionControlCommand:       "Traction Control Command",
	MTITractionControlReply:         "Traction Control Reply",
	MTISimpleNodeIdentInfoRequest:   "SNIP Request",
	MTISimpleNodeIdentInfoReply:     "SNIP Reply",
	MTIDatagram:                     "Datagram",
	MTIDatagramOK:                   "Datagram OK",
	MTIDatagramRejected:             "Datagram Rejected",
	MTIStreamInitiateRequest:        "Stream Initiate Request",
	MTIStreamInitiateReply:          "Stream Initiate Reply",
	MTIStreamData:                   "Stream Data",
	MTIStreamDataProceed:            "Stream Data Proceed",
	MTIStreamDataComplete:           "Stream Data Complete",
}

// String returns the message name, or the hex value for an unknown MTI.
func (m MTI) String() string {
	if name, ok := mtiNames[m]; ok {
		return name
	}
	return fmt.Sprintf("MTI 0x%03X", uint16(m))
}

package openlcb

// Well-known event IDs from the Event Identifiers standard.
const (
	EventEmergencyOff       EventID = 0x010000000000FFFF
	EventClearEmergencyOff  EventID = 0x010000000000FFFE
	EventEmergencyStop      EventID = 0x010000000000FFFD
	EventClearEmergencyStop EventID = 0x010000000000FFFC
	EventNewLogEntry        EventID = 0x010000000000FFF8
	EventIdentButtonCombo   EventID = 0x010000000000FE00
	EventLinkErrorCode1     EventID = 0x010000000000FD01
	EventLinkErrorCode2     EventID = 0x010000000000FD02
	EventLinkErrorCode3     EventID = 0x010000000000FD03
	EventLinkErrorCode4     EventID = 0x010000000000FD04
	EventDuplicateNodeID    EventID = 0x0101000000000201
)

var wellKnownEventNames = map[EventID]string{
	EventEmergencyOff:       "Emergency Off",
	EventClearEmergencyOff:  "Clear Emergency Off",
	EventEmergencyStop:      "Emergency Stop",
	EventClearEmergencyStop: "Clear Emergency Stop",
	EventNewLogEntry:        "New Log Entry",
	EventIdentButtonCombo:   "Ident Button Combination",
	EventLinkErrorCode1:     "Link Error Code 1",
	EventLinkErrorCode2:     "Link Error Code 2",
	EventLinkErrorCode3:     "Link Error Code 3",
	EventLinkErrorCode4:     "Link Error Code 4",
	EventDuplicateNodeID:    "Duplicate Node ID Detected",
}

// WellKnownName returns the standard name of a well-known event ID, or
// "" for any other event.
func WellKnownName(id EventID) string {
	return wellKnownEventNames[id]
}

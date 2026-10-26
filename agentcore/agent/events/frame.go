package events

// Frame is one Server-Sent Events frame produced by a mapper: an optional
// event name and its data. A transport writes it as "event: <Event>" (when
// set) followed by "data: <Data>".
type Frame struct {
	Event string
	Data  []byte
}

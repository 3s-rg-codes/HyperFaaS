package stats

import "time"

type UpdateType int
type UpdateEvent int
type UpdateStatus int

const (
	TypeContainer UpdateType = iota
)

const (
	EventResponse UpdateEvent = iota
	EventDown
	EventTimeout
	EventStart
	EventStop
	EventCall
	EventRunning
)

const (
	StatusSuccess UpdateStatus = iota
	StatusFailed
)

func (t UpdateType) String() string {
	return [...]string{"container"}[t]
}

func (e UpdateEvent) String() string {
	return [...]string{"response", "down", "timeout", "start", "stop", "call", "running"}[e]
}

func (s UpdateStatus) String() string {
	return [...]string{"success", "failed"}[s]
}

type StatusUpdate struct {
	InstanceID string
	Timestamp  time.Time
	Type       UpdateType
	Event      UpdateEvent
	Status     UpdateStatus
	FunctionID string
}

func Event() *StatusUpdate {
	return &StatusUpdate{Timestamp: time.Now().UTC().Truncate(time.Nanosecond)}
}

func (su *StatusUpdate) Container(instanceID string) *StatusUpdate {
	su.InstanceID = instanceID
	su.Type = TypeContainer
	return su
}

// Not forced due to backwards compatibility with controller.go and the Call API
// TODO: Make functionID required and refactor the Call API to require functionID
func (su *StatusUpdate) Function(functionID string) *StatusUpdate {
	su.FunctionID = functionID
	return su
}

func (su *StatusUpdate) Response() *StatusUpdate {
	su.Event = EventResponse
	return su
}

func (su *StatusUpdate) Down() *StatusUpdate {
	su.Event = EventDown
	return su
}

func (su *StatusUpdate) Timeout() *StatusUpdate {
	su.Event = EventTimeout
	return su
}

func (su *StatusUpdate) Start() *StatusUpdate {
	su.Event = EventStart
	return su
}

func (su *StatusUpdate) Stop() *StatusUpdate {
	su.Event = EventStop
	return su
}

func (su *StatusUpdate) Call() *StatusUpdate {
	su.Event = EventCall
	return su
}

func (su *StatusUpdate) Success() *StatusUpdate {
	su.Status = StatusSuccess
	return su
}

func (su *StatusUpdate) Failed() *StatusUpdate {
	su.Status = StatusFailed
	return su
}

func (su *StatusUpdate) Running() *StatusUpdate {
	su.Event = EventRunning
	return su
}

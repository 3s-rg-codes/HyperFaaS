package stats

type StatusUpdate struct {
	InstanceID string
	Type       string
	Event      string
	Status     string
	FunctionID string
}

func Event() *StatusUpdate {
	return &StatusUpdate{}
}

func (su *StatusUpdate) Container(instanceID string) *StatusUpdate {
	su.InstanceID = instanceID
	su.Type = "container"
	return su
}

// Not forced due to backwards compatibility with controller.go and the Call API
// TODO: Make functionID required and refactor the Call API to require functionID
func (su *StatusUpdate) Function(functionID string) *StatusUpdate {
	su.FunctionID = functionID
	return su
}

func (su *StatusUpdate) Response() *StatusUpdate {
	su.Event = "response"
	return su
}

func (su *StatusUpdate) Die() *StatusUpdate {
	su.Event = "die"
	return su
}

func (su *StatusUpdate) Timeout() *StatusUpdate {
	su.Event = "timeout"
	return su
}

func (su *StatusUpdate) Start() *StatusUpdate {
	su.Event = "start"
	return su
}

func (su *StatusUpdate) Stop() *StatusUpdate {
	su.Event = "stop"
	return su
}

func (su *StatusUpdate) Call() *StatusUpdate {
	su.Event = "call"
	return su
}

func (su *StatusUpdate) WithStatus(status string) *StatusUpdate {
	su.Status = status
	return su
}

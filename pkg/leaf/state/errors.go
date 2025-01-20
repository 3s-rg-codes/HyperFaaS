package state

import "fmt"

type FunctionNotRegisteredError struct {
	FunctionID FunctionID
}

func (e *FunctionNotRegisteredError) Error() string {
	return fmt.Sprintf("function %s not registered with any worker", e.FunctionID)
}

type NoIdleInstanceError struct {
	FunctionID FunctionID
}

func (e *NoIdleInstanceError) Error() string {
	return fmt.Sprintf("no idle instance found for function %s", e.FunctionID)
}

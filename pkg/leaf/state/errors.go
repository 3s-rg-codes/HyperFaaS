package state

import "fmt"

type FunctionNotAssignedError struct {
	FunctionID FunctionID
}

func (e *FunctionNotAssignedError) Error() string {
	return fmt.Sprintf("function %s not registered with any worker", e.FunctionID)
}

type FunctionAlreadyAssignedError struct {
	FunctionID FunctionID
}

func (e *FunctionAlreadyAssignedError) Error() string {
	return fmt.Sprintf("function %s already assigned to worker", e.FunctionID)
}

type InstanceNotFoundError struct {
	InstanceID InstanceID
}

func (e *InstanceNotFoundError) Error() string {
	return fmt.Sprintf("instance %s not found", e.InstanceID)
}

type NoIdleInstanceError struct {
	FunctionID FunctionID
}

func (e *NoIdleInstanceError) Error() string {
	return fmt.Sprintf("no idle instance found for function %s", e.FunctionID)
}

type WorkerNotFoundError struct {
	WorkerID WorkerID
}

func (e *WorkerNotFoundError) Error() string {
	return fmt.Sprintf("worker %s not found", e.WorkerID)
}

type TooManyStartingInstancesError struct {
	FunctionID FunctionID
}

func (e *TooManyStartingInstancesError) Error() string {
	return fmt.Sprintf("too many starting instances for function %s", e.FunctionID)
}

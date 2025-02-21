package controller

import "fmt"

type ContainerCrashError struct {
	InstanceID     string
	ContainerError string
}

func (e *ContainerCrashError) Error() string {
	return fmt.Sprintf("user container crashed for instance ID: %s, error: %s", e.InstanceID, e.ContainerError)
}

type InstanceNotFoundError struct {
	InstanceID string
}

func (e *InstanceNotFoundError) Error() string {
	return fmt.Sprintf("instance not found for instance ID: %s", e.InstanceID)
}

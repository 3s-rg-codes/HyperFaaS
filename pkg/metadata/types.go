package metadata

import (
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
)

// FunctionMetadata represents the data required to run a function.
type FunctionMetadata struct {
	ID     string
	Image  *common.Image
	Config *common.Config
}

// EventType provides the type of change observed in the metadata store.
type EventType int

const (
	EventTypeUnknown EventType = iota
	EventTypePut
	EventTypeDelete
)

// Event encapsulates metadata change notifications.
type Event struct {
	Type       EventType
	FunctionID string
	Function   *FunctionMetadata
}

// ListResult holds the metadata snapshot together with the revision used for future watches.
type ListResult struct {
	Functions []*FunctionMetadata
	Revision  int64
}

// Options configures the metadata client.
type Options struct {
	// Prefix controls where function metadata is stored. Defaults to DefaultPrefix when empty.
	Prefix string
	// DialTimeout overrides the etcd dial timeout. Zero uses DefaultDialTimeout.
	DialTimeout time.Duration
}

const (
	DefaultPrefix      = "hyperfaas/functions"
	DefaultDialTimeout = 5 * time.Second
)

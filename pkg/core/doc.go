// Package core holds shared protobuf-generated domain types and enums used across HyperFaaS components.
//
// Timeouts: RuntimeSpec.execution_timeout caps how long a function instance may run once invoked.
// ScalePolicySpec.request_timeout caps end-to-end client wait inside HyperFaaS (routing, queueing, cold start, and execution combined).
//
// Routing state is published through leaf.proto RoutingStateFrame, not a core
// message, so the routing stream can carry an explicit projection.
package core

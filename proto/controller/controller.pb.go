// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.0
// 	protoc        v5.29.3
// source: controller/controller.proto

package controller

import (
	common "github.com/3s-rg-codes/HyperFaaS/proto/common"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type ImageTag struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Tag           string                 `protobuf:"bytes,1,opt,name=tag,proto3" json:"tag,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ImageTag) Reset() {
	*x = ImageTag{}
	mi := &file_controller_controller_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ImageTag) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ImageTag) ProtoMessage() {}

func (x *ImageTag) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ImageTag.ProtoReflect.Descriptor instead.
func (*ImageTag) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{0}
}

func (x *ImageTag) GetTag() string {
	if x != nil {
		return x.Tag
	}
	return ""
}

type Config struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// container memory limit in bytes
	Memory        int64      `protobuf:"varint,1,opt,name=memory,proto3" json:"memory,omitempty"`
	Cpu           *CPUConfig `protobuf:"bytes,2,opt,name=cpu,proto3" json:"cpu,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Config) Reset() {
	*x = Config{}
	mi := &file_controller_controller_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Config) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Config) ProtoMessage() {}

func (x *Config) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Config.ProtoReflect.Descriptor instead.
func (*Config) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{1}
}

func (x *Config) GetMemory() int64 {
	if x != nil {
		return x.Memory
	}
	return 0
}

func (x *Config) GetCpu() *CPUConfig {
	if x != nil {
		return x.Cpu
	}
	return nil
}

// Container CPU configuration. If the host has 2 CPUs and the container should only use 1 CPU, set period to 100000 and quota to 50000.
type CPUConfig struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// CPU CFS (Completely Fair Scheduler) period
	Period int64 `protobuf:"varint,1,opt,name=period,proto3" json:"period,omitempty"`
	// CPU CFS (Completely Fair Scheduler) quota
	Quota         int64 `protobuf:"varint,2,opt,name=quota,proto3" json:"quota,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CPUConfig) Reset() {
	*x = CPUConfig{}
	mi := &file_controller_controller_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CPUConfig) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CPUConfig) ProtoMessage() {}

func (x *CPUConfig) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CPUConfig.ProtoReflect.Descriptor instead.
func (*CPUConfig) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{2}
}

func (x *CPUConfig) GetPeriod() int64 {
	if x != nil {
		return x.Period
	}
	return 0
}

func (x *CPUConfig) GetQuota() int64 {
	if x != nil {
		return x.Quota
	}
	return 0
}

type StartRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	FunctionId    string                 `protobuf:"bytes,1,opt,name=function_id,json=functionId,proto3" json:"function_id,omitempty"`
	ImageTag      *ImageTag              `protobuf:"bytes,2,opt,name=image_tag,json=imageTag,proto3" json:"image_tag,omitempty"`
	Config        *Config                `protobuf:"bytes,3,opt,name=config,proto3" json:"config,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StartRequest) Reset() {
	*x = StartRequest{}
	mi := &file_controller_controller_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StartRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StartRequest) ProtoMessage() {}

func (x *StartRequest) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StartRequest.ProtoReflect.Descriptor instead.
func (*StartRequest) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{3}
}

func (x *StartRequest) GetFunctionId() string {
	if x != nil {
		return x.FunctionId
	}
	return ""
}

func (x *StartRequest) GetImageTag() *ImageTag {
	if x != nil {
		return x.ImageTag
	}
	return nil
}

func (x *StartRequest) GetConfig() *Config {
	if x != nil {
		return x.Config
	}
	return nil
}

type StatusUpdate struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	InstanceId    string                 `protobuf:"bytes,1,opt,name=instance_id,json=instanceId,proto3" json:"instance_id,omitempty"`
	Type          string                 `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Event         string                 `protobuf:"bytes,3,opt,name=event,proto3" json:"event,omitempty"`
	Status        string                 `protobuf:"bytes,4,opt,name=status,proto3" json:"status,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StatusUpdate) Reset() {
	*x = StatusUpdate{}
	mi := &file_controller_controller_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StatusUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StatusUpdate) ProtoMessage() {}

func (x *StatusUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StatusUpdate.ProtoReflect.Descriptor instead.
func (*StatusUpdate) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{4}
}

func (x *StatusUpdate) GetInstanceId() string {
	if x != nil {
		return x.InstanceId
	}
	return ""
}

func (x *StatusUpdate) GetType() string {
	if x != nil {
		return x.Type
	}
	return ""
}

func (x *StatusUpdate) GetEvent() string {
	if x != nil {
		return x.Event
	}
	return ""
}

func (x *StatusUpdate) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

type StatusRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	NodeID        string                 `protobuf:"bytes,1,opt,name=nodeID,proto3" json:"nodeID,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StatusRequest) Reset() {
	*x = StatusRequest{}
	mi := &file_controller_controller_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StatusRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StatusRequest) ProtoMessage() {}

func (x *StatusRequest) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StatusRequest.ProtoReflect.Descriptor instead.
func (*StatusRequest) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{5}
}

func (x *StatusRequest) GetNodeID() string {
	if x != nil {
		return x.NodeID
	}
	return ""
}

type MetricsRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	NodeID        string                 `protobuf:"bytes,1,opt,name=nodeID,proto3" json:"nodeID,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *MetricsRequest) Reset() {
	*x = MetricsRequest{}
	mi := &file_controller_controller_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MetricsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MetricsRequest) ProtoMessage() {}

func (x *MetricsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MetricsRequest.ProtoReflect.Descriptor instead.
func (*MetricsRequest) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{6}
}

func (x *MetricsRequest) GetNodeID() string {
	if x != nil {
		return x.NodeID
	}
	return ""
}

type MetricsUpdate struct {
	state            protoimpl.MessageState `protogen:"open.v1"`
	UsedRamPercent   float64                `protobuf:"fixed64,1,opt,name=used_ram_percent,json=usedRamPercent,proto3" json:"used_ram_percent,omitempty"`
	CpuPercentPercpu []float64              `protobuf:"fixed64,2,rep,packed,name=cpu_percent_percpu,json=cpuPercentPercpu,proto3" json:"cpu_percent_percpu,omitempty"`
	unknownFields    protoimpl.UnknownFields
	sizeCache        protoimpl.SizeCache
}

func (x *MetricsUpdate) Reset() {
	*x = MetricsUpdate{}
	mi := &file_controller_controller_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *MetricsUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MetricsUpdate) ProtoMessage() {}

func (x *MetricsUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MetricsUpdate.ProtoReflect.Descriptor instead.
func (*MetricsUpdate) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{7}
}

func (x *MetricsUpdate) GetUsedRamPercent() float64 {
	if x != nil {
		return x.UsedRamPercent
	}
	return 0
}

func (x *MetricsUpdate) GetCpuPercentPercpu() []float64 {
	if x != nil {
		return x.CpuPercentPercpu
	}
	return nil
}

type InstanceState struct {
	state      protoimpl.MessageState `protogen:"open.v1"`
	InstanceId string                 `protobuf:"bytes,1,opt,name=instance_id,json=instanceId,proto3" json:"instance_id,omitempty"`
	IsActive   bool                   `protobuf:"varint,2,opt,name=is_active,json=isActive,proto3" json:"is_active,omitempty"`
	// Duration fields stored in milliseconds
	Lastworked    *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=lastworked,proto3" json:"lastworked,omitempty"`
	Created       *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=created,proto3" json:"created,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *InstanceState) Reset() {
	*x = InstanceState{}
	mi := &file_controller_controller_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InstanceState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InstanceState) ProtoMessage() {}

func (x *InstanceState) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InstanceState.ProtoReflect.Descriptor instead.
func (*InstanceState) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{8}
}

func (x *InstanceState) GetInstanceId() string {
	if x != nil {
		return x.InstanceId
	}
	return ""
}

func (x *InstanceState) GetIsActive() bool {
	if x != nil {
		return x.IsActive
	}
	return false
}

func (x *InstanceState) GetLastworked() *timestamppb.Timestamp {
	if x != nil {
		return x.Lastworked
	}
	return nil
}

func (x *InstanceState) GetCreated() *timestamppb.Timestamp {
	if x != nil {
		return x.Created
	}
	return nil
}

type FunctionState struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	FunctionId    string                 `protobuf:"bytes,1,opt,name=function_id,json=functionId,proto3" json:"function_id,omitempty"`
	Running       []*InstanceState       `protobuf:"bytes,2,rep,name=running,proto3" json:"running,omitempty"`
	Idle          []*InstanceState       `protobuf:"bytes,3,rep,name=idle,proto3" json:"idle,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *FunctionState) Reset() {
	*x = FunctionState{}
	mi := &file_controller_controller_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FunctionState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FunctionState) ProtoMessage() {}

func (x *FunctionState) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FunctionState.ProtoReflect.Descriptor instead.
func (*FunctionState) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{9}
}

func (x *FunctionState) GetFunctionId() string {
	if x != nil {
		return x.FunctionId
	}
	return ""
}

func (x *FunctionState) GetRunning() []*InstanceState {
	if x != nil {
		return x.Running
	}
	return nil
}

func (x *FunctionState) GetIdle() []*InstanceState {
	if x != nil {
		return x.Idle
	}
	return nil
}

type StateResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// List of function states for this worker
	Functions     []*FunctionState `protobuf:"bytes,1,rep,name=functions,proto3" json:"functions,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StateResponse) Reset() {
	*x = StateResponse{}
	mi := &file_controller_controller_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StateResponse) ProtoMessage() {}

func (x *StateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StateResponse.ProtoReflect.Descriptor instead.
func (*StateResponse) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{10}
}

func (x *StateResponse) GetFunctions() []*FunctionState {
	if x != nil {
		return x.Functions
	}
	return nil
}

type StateRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	NodeId        string                 `protobuf:"bytes,1,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StateRequest) Reset() {
	*x = StateRequest{}
	mi := &file_controller_controller_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StateRequest) ProtoMessage() {}

func (x *StateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_controller_controller_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StateRequest.ProtoReflect.Descriptor instead.
func (*StateRequest) Descriptor() ([]byte, []int) {
	return file_controller_controller_proto_rawDescGZIP(), []int{11}
}

func (x *StateRequest) GetNodeId() string {
	if x != nil {
		return x.NodeId
	}
	return ""
}

var File_controller_controller_proto protoreflect.FileDescriptor

var file_controller_controller_proto_rawDesc = []byte{
	0x0a, 0x1b, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2f, 0x63, 0x6f, 0x6e,
	0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x63,
	0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x1a, 0x13, 0x63, 0x6f, 0x6d, 0x6d, 0x6f,
	0x6e, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1f,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f,
	0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22,
	0x1c, 0x0a, 0x08, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x54, 0x61, 0x67, 0x12, 0x10, 0x0a, 0x03, 0x74,
	0x61, 0x67, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x74, 0x61, 0x67, 0x22, 0x49, 0x0a,
	0x06, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x12, 0x16, 0x0a, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72,
	0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x12,
	0x27, 0x0a, 0x03, 0x63, 0x70, 0x75, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x63,
	0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x43, 0x50, 0x55, 0x43, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x52, 0x03, 0x63, 0x70, 0x75, 0x22, 0x39, 0x0a, 0x09, 0x43, 0x50, 0x55, 0x43,
	0x6f, 0x6e, 0x66, 0x69, 0x67, 0x12, 0x16, 0x0a, 0x06, 0x70, 0x65, 0x72, 0x69, 0x6f, 0x64, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x06, 0x70, 0x65, 0x72, 0x69, 0x6f, 0x64, 0x12, 0x14, 0x0a,
	0x05, 0x71, 0x75, 0x6f, 0x74, 0x61, 0x18, 0x02, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x71, 0x75,
	0x6f, 0x74, 0x61, 0x22, 0x8e, 0x01, 0x0a, 0x0c, 0x53, 0x74, 0x61, 0x72, 0x74, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e,
	0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x66, 0x75, 0x6e, 0x63, 0x74,
	0x69, 0x6f, 0x6e, 0x49, 0x64, 0x12, 0x31, 0x0a, 0x09, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x5f, 0x74,
	0x61, 0x67, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72,
	0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x54, 0x61, 0x67, 0x52, 0x08,
	0x69, 0x6d, 0x61, 0x67, 0x65, 0x54, 0x61, 0x67, 0x12, 0x2a, 0x0a, 0x06, 0x63, 0x6f, 0x6e, 0x66,
	0x69, 0x67, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72,
	0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x52, 0x06, 0x63, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x22, 0x71, 0x0a, 0x0c, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x55, 0x70,
	0x64, 0x61, 0x74, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x69, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65,
	0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x69, 0x6e, 0x73, 0x74, 0x61,
	0x6e, 0x63, 0x65, 0x49, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x76, 0x65,
	0x6e, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x12,
	0x16, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x22, 0x27, 0x0a, 0x0d, 0x53, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x6e, 0x6f, 0x64, 0x65,
	0x49, 0x44, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6e, 0x6f, 0x64, 0x65, 0x49, 0x44,
	0x22, 0x28, 0x0a, 0x0e, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x6e, 0x6f, 0x64, 0x65, 0x49, 0x44, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x06, 0x6e, 0x6f, 0x64, 0x65, 0x49, 0x44, 0x22, 0x67, 0x0a, 0x0d, 0x4d, 0x65,
	0x74, 0x72, 0x69, 0x63, 0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x28, 0x0a, 0x10, 0x75,
	0x73, 0x65, 0x64, 0x5f, 0x72, 0x61, 0x6d, 0x5f, 0x70, 0x65, 0x72, 0x63, 0x65, 0x6e, 0x74, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x01, 0x52, 0x0e, 0x75, 0x73, 0x65, 0x64, 0x52, 0x61, 0x6d, 0x50, 0x65,
	0x72, 0x63, 0x65, 0x6e, 0x74, 0x12, 0x2c, 0x0a, 0x12, 0x63, 0x70, 0x75, 0x5f, 0x70, 0x65, 0x72,
	0x63, 0x65, 0x6e, 0x74, 0x5f, 0x70, 0x65, 0x72, 0x63, 0x70, 0x75, 0x18, 0x02, 0x20, 0x03, 0x28,
	0x01, 0x52, 0x10, 0x63, 0x70, 0x75, 0x50, 0x65, 0x72, 0x63, 0x65, 0x6e, 0x74, 0x50, 0x65, 0x72,
	0x63, 0x70, 0x75, 0x22, 0xbf, 0x01, 0x0a, 0x0d, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x69, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63,
	0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x69, 0x6e, 0x73, 0x74,
	0x61, 0x6e, 0x63, 0x65, 0x49, 0x64, 0x12, 0x1b, 0x0a, 0x09, 0x69, 0x73, 0x5f, 0x61, 0x63, 0x74,
	0x69, 0x76, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x08, 0x52, 0x08, 0x69, 0x73, 0x41, 0x63, 0x74,
	0x69, 0x76, 0x65, 0x12, 0x3a, 0x0a, 0x0a, 0x6c, 0x61, 0x73, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x65,
	0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74,
	0x61, 0x6d, 0x70, 0x52, 0x0a, 0x6c, 0x61, 0x73, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x65, 0x64, 0x12,
	0x34, 0x0a, 0x07, 0x63, 0x72, 0x65, 0x61, 0x74, 0x65, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62,
	0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x07, 0x63, 0x72,
	0x65, 0x61, 0x74, 0x65, 0x64, 0x22, 0x94, 0x01, 0x0a, 0x0d, 0x46, 0x75, 0x6e, 0x63, 0x74, 0x69,
	0x6f, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x66, 0x75, 0x6e, 0x63, 0x74,
	0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x66, 0x75,
	0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x64, 0x12, 0x33, 0x0a, 0x07, 0x72, 0x75, 0x6e, 0x6e,
	0x69, 0x6e, 0x67, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x63, 0x6f, 0x6e, 0x74,
	0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x52, 0x07, 0x72, 0x75, 0x6e, 0x6e, 0x69, 0x6e, 0x67, 0x12, 0x2d, 0x0a,
	0x04, 0x69, 0x64, 0x6c, 0x65, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x63, 0x6f,
	0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63,
	0x65, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x04, 0x69, 0x64, 0x6c, 0x65, 0x22, 0x48, 0x0a, 0x0d,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x37, 0x0a,
	0x09, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x19, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x46, 0x75,
	0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x09, 0x66, 0x75, 0x6e,
	0x63, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x22, 0x27, 0x0a, 0x0c, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x17, 0x0a, 0x07, 0x6e, 0x6f, 0x64, 0x65, 0x5f, 0x69,
	0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6e, 0x6f, 0x64, 0x65, 0x49, 0x64, 0x32,
	0xe7, 0x02, 0x0a, 0x0a, 0x43, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x12, 0x35,
	0x0a, 0x05, 0x53, 0x74, 0x61, 0x72, 0x74, 0x12, 0x18, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f,
	0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x53, 0x74, 0x61, 0x72, 0x74, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x12, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x49, 0x6e, 0x73, 0x74, 0x61,
	0x6e, 0x63, 0x65, 0x49, 0x44, 0x12, 0x31, 0x0a, 0x04, 0x43, 0x61, 0x6c, 0x6c, 0x12, 0x13, 0x2e,
	0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x43, 0x61, 0x6c, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x14, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x43, 0x61, 0x6c, 0x6c,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2e, 0x0a, 0x04, 0x53, 0x74, 0x6f, 0x70,
	0x12, 0x12, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e,
	0x63, 0x65, 0x49, 0x44, 0x1a, 0x12, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x49, 0x6e,
	0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x49, 0x44, 0x12, 0x3f, 0x0a, 0x06, 0x53, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x12, 0x19, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e,
	0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x53, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x30, 0x01, 0x12, 0x40, 0x0a, 0x07, 0x4d, 0x65, 0x74,
	0x72, 0x69, 0x63, 0x73, 0x12, 0x1a, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65,
	0x72, 0x2e, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x19, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x4d, 0x65,
	0x74, 0x72, 0x69, 0x63, 0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x3c, 0x0a, 0x05, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x12, 0x18, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65,
	0x72, 0x2e, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x19,
	0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2e, 0x53, 0x74, 0x61, 0x74,
	0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42, 0x33, 0x5a, 0x31, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x33, 0x73, 0x2d, 0x72, 0x67, 0x2d, 0x63, 0x6f,
	0x64, 0x65, 0x73, 0x2f, 0x48, 0x79, 0x70, 0x65, 0x72, 0x46, 0x61, 0x61, 0x53, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_controller_controller_proto_rawDescOnce sync.Once
	file_controller_controller_proto_rawDescData = file_controller_controller_proto_rawDesc
)

func file_controller_controller_proto_rawDescGZIP() []byte {
	file_controller_controller_proto_rawDescOnce.Do(func() {
		file_controller_controller_proto_rawDescData = protoimpl.X.CompressGZIP(file_controller_controller_proto_rawDescData)
	})
	return file_controller_controller_proto_rawDescData
}

var file_controller_controller_proto_msgTypes = make([]protoimpl.MessageInfo, 12)
var file_controller_controller_proto_goTypes = []any{
	(*ImageTag)(nil),              // 0: controller.ImageTag
	(*Config)(nil),                // 1: controller.Config
	(*CPUConfig)(nil),             // 2: controller.CPUConfig
	(*StartRequest)(nil),          // 3: controller.StartRequest
	(*StatusUpdate)(nil),          // 4: controller.StatusUpdate
	(*StatusRequest)(nil),         // 5: controller.StatusRequest
	(*MetricsRequest)(nil),        // 6: controller.MetricsRequest
	(*MetricsUpdate)(nil),         // 7: controller.MetricsUpdate
	(*InstanceState)(nil),         // 8: controller.InstanceState
	(*FunctionState)(nil),         // 9: controller.FunctionState
	(*StateResponse)(nil),         // 10: controller.StateResponse
	(*StateRequest)(nil),          // 11: controller.StateRequest
	(*timestamppb.Timestamp)(nil), // 12: google.protobuf.Timestamp
	(*common.CallRequest)(nil),    // 13: common.CallRequest
	(*common.InstanceID)(nil),     // 14: common.InstanceID
	(*common.CallResponse)(nil),   // 15: common.CallResponse
}
var file_controller_controller_proto_depIdxs = []int32{
	2,  // 0: controller.Config.cpu:type_name -> controller.CPUConfig
	0,  // 1: controller.StartRequest.image_tag:type_name -> controller.ImageTag
	1,  // 2: controller.StartRequest.config:type_name -> controller.Config
	12, // 3: controller.InstanceState.lastworked:type_name -> google.protobuf.Timestamp
	12, // 4: controller.InstanceState.created:type_name -> google.protobuf.Timestamp
	8,  // 5: controller.FunctionState.running:type_name -> controller.InstanceState
	8,  // 6: controller.FunctionState.idle:type_name -> controller.InstanceState
	9,  // 7: controller.StateResponse.functions:type_name -> controller.FunctionState
	3,  // 8: controller.Controller.Start:input_type -> controller.StartRequest
	13, // 9: controller.Controller.Call:input_type -> common.CallRequest
	14, // 10: controller.Controller.Stop:input_type -> common.InstanceID
	5,  // 11: controller.Controller.Status:input_type -> controller.StatusRequest
	6,  // 12: controller.Controller.Metrics:input_type -> controller.MetricsRequest
	11, // 13: controller.Controller.State:input_type -> controller.StateRequest
	14, // 14: controller.Controller.Start:output_type -> common.InstanceID
	15, // 15: controller.Controller.Call:output_type -> common.CallResponse
	14, // 16: controller.Controller.Stop:output_type -> common.InstanceID
	4,  // 17: controller.Controller.Status:output_type -> controller.StatusUpdate
	7,  // 18: controller.Controller.Metrics:output_type -> controller.MetricsUpdate
	10, // 19: controller.Controller.State:output_type -> controller.StateResponse
	14, // [14:20] is the sub-list for method output_type
	8,  // [8:14] is the sub-list for method input_type
	8,  // [8:8] is the sub-list for extension type_name
	8,  // [8:8] is the sub-list for extension extendee
	0,  // [0:8] is the sub-list for field type_name
}

func init() { file_controller_controller_proto_init() }
func file_controller_controller_proto_init() {
	if File_controller_controller_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_controller_controller_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   12,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_controller_controller_proto_goTypes,
		DependencyIndexes: file_controller_controller_proto_depIdxs,
		MessageInfos:      file_controller_controller_proto_msgTypes,
	}.Build()
	File_controller_controller_proto = out.File
	file_controller_controller_proto_rawDesc = nil
	file_controller_controller_proto_goTypes = nil
	file_controller_controller_proto_depIdxs = nil
}

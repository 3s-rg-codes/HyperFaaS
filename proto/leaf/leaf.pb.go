// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v5.27.0
// source: leaf/leaf.proto

package leaf

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type FindInstanceRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	FunctionId string `protobuf:"bytes,1,opt,name=function_id,json=functionId,proto3" json:"function_id,omitempty"`
}

func (x *FindInstanceRequest) Reset() {
	*x = FindInstanceRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_leaf_leaf_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *FindInstanceRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FindInstanceRequest) ProtoMessage() {}

func (x *FindInstanceRequest) ProtoReflect() protoreflect.Message {
	mi := &file_leaf_leaf_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FindInstanceRequest.ProtoReflect.Descriptor instead.
func (*FindInstanceRequest) Descriptor() ([]byte, []int) {
	return file_leaf_leaf_proto_rawDescGZIP(), []int{0}
}

func (x *FindInstanceRequest) GetFunctionId() string {
	if x != nil {
		return x.FunctionId
	}
	return ""
}

type FindInstanceResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	InstanceId string `protobuf:"bytes,1,opt,name=instance_id,json=instanceId,proto3" json:"instance_id,omitempty"`
	WorkerIp   string `protobuf:"bytes,2,opt,name=worker_ip,json=workerIp,proto3" json:"worker_ip,omitempty"`
}

func (x *FindInstanceResponse) Reset() {
	*x = FindInstanceResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_leaf_leaf_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *FindInstanceResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FindInstanceResponse) ProtoMessage() {}

func (x *FindInstanceResponse) ProtoReflect() protoreflect.Message {
	mi := &file_leaf_leaf_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FindInstanceResponse.ProtoReflect.Descriptor instead.
func (*FindInstanceResponse) Descriptor() ([]byte, []int) {
	return file_leaf_leaf_proto_rawDescGZIP(), []int{1}
}

func (x *FindInstanceResponse) GetInstanceId() string {
	if x != nil {
		return x.InstanceId
	}
	return ""
}

func (x *FindInstanceResponse) GetWorkerIp() string {
	if x != nil {
		return x.WorkerIp
	}
	return ""
}

var File_leaf_leaf_proto protoreflect.FileDescriptor

var file_leaf_leaf_proto_rawDesc = []byte{
	0x0a, 0x0f, 0x6c, 0x65, 0x61, 0x66, 0x2f, 0x6c, 0x65, 0x61, 0x66, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x04, 0x6c, 0x65, 0x61, 0x66, 0x22, 0x36, 0x0a, 0x13, 0x46, 0x69, 0x6e, 0x64, 0x49,
	0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1f,
	0x0a, 0x0b, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0a, 0x66, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x64, 0x22,
	0x54, 0x0a, 0x14, 0x46, 0x69, 0x6e, 0x64, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x69, 0x6e, 0x73, 0x74, 0x61,
	0x6e, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x69, 0x6e,
	0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x49, 0x64, 0x12, 0x1b, 0x0a, 0x09, 0x77, 0x6f, 0x72, 0x6b,
	0x65, 0x72, 0x5f, 0x69, 0x70, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x77, 0x6f, 0x72,
	0x6b, 0x65, 0x72, 0x49, 0x70, 0x32, 0x4d, 0x0a, 0x04, 0x4c, 0x65, 0x61, 0x66, 0x12, 0x45, 0x0a,
	0x0c, 0x46, 0x69, 0x6e, 0x64, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x12, 0x19, 0x2e,
	0x6c, 0x65, 0x61, 0x66, 0x2e, 0x46, 0x69, 0x6e, 0x64, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63,
	0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1a, 0x2e, 0x6c, 0x65, 0x61, 0x66, 0x2e,
	0x46, 0x69, 0x6e, 0x64, 0x49, 0x6e, 0x73, 0x74, 0x61, 0x6e, 0x63, 0x65, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x42, 0x2d, 0x5a, 0x2b, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x33, 0x73, 0x2d, 0x72, 0x67, 0x2d, 0x63, 0x6f, 0x64, 0x65, 0x73, 0x2f, 0x48,
	0x79, 0x70, 0x65, 0x72, 0x46, 0x61, 0x61, 0x53, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x6c,
	0x65, 0x61, 0x66, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_leaf_leaf_proto_rawDescOnce sync.Once
	file_leaf_leaf_proto_rawDescData = file_leaf_leaf_proto_rawDesc
)

func file_leaf_leaf_proto_rawDescGZIP() []byte {
	file_leaf_leaf_proto_rawDescOnce.Do(func() {
		file_leaf_leaf_proto_rawDescData = protoimpl.X.CompressGZIP(file_leaf_leaf_proto_rawDescData)
	})
	return file_leaf_leaf_proto_rawDescData
}

var file_leaf_leaf_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_leaf_leaf_proto_goTypes = []interface{}{
	(*FindInstanceRequest)(nil),  // 0: leaf.FindInstanceRequest
	(*FindInstanceResponse)(nil), // 1: leaf.FindInstanceResponse
}
var file_leaf_leaf_proto_depIdxs = []int32{
	0, // 0: leaf.Leaf.FindInstance:input_type -> leaf.FindInstanceRequest
	1, // 1: leaf.Leaf.FindInstance:output_type -> leaf.FindInstanceResponse
	1, // [1:2] is the sub-list for method output_type
	0, // [0:1] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_leaf_leaf_proto_init() }
func file_leaf_leaf_proto_init() {
	if File_leaf_leaf_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_leaf_leaf_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*FindInstanceRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_leaf_leaf_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*FindInstanceResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_leaf_leaf_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_leaf_leaf_proto_goTypes,
		DependencyIndexes: file_leaf_leaf_proto_depIdxs,
		MessageInfos:      file_leaf_leaf_proto_msgTypes,
	}.Build()
	File_leaf_leaf_proto = out.File
	file_leaf_leaf_proto_rawDesc = nil
	file_leaf_leaf_proto_goTypes = nil
	file_leaf_leaf_proto_depIdxs = nil
}
import grpc from 'k6/net/grpc';
import { check } from 'k6';

// K6 GRPC client
const client = new grpc.Client();

// Load all proto files
const protoFiles = __PROTO_FILE__;
client.load(['../config'], ...protoFiles);

// K6 Options
export const options = __SCENARIOS__;

// Service call functions
__EXEC_FUNCTIONS__

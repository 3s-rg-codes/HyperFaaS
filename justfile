# For more information, see https://github.com/casey/just

set dotenv-load
set export
set shell := ["bash", "-c"]

default:
  @just --list

proto:
    protoc --proto_path=proto "proto/function/function.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/controller/controller.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative

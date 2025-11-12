# For more information, see https://github.com/casey/just

set dotenv-load
set export
set windows-shell := ["powershell"]
set shell := ["bash", "-c"]

default:
  @just --list --unsorted

############################
# Building Stuff
############################

# generate the proto files
proto:
    @echo "Generating proto files"
    protoc --proto_path=proto "proto/function/function.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/worker/worker.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/leaf/leaf.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/common/common.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/routingcontroller/routingcontroller.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative

# build all go functions
build-functions-go:
    find ./functions/go/*/ -maxdepth 0 -type d | xargs -I {} bash -c 'just build-function-go $(basename "{}")'

# build a single go function
build-function-go function_name:
    docker build -t hyperfaas-{{function_name}} -f ./docker/function.Dockerfile --build-arg FUNCTION_NAME="{{function_name}}" .

############################
# Running Stuff
############################

run-local-worker:
    @echo "Running local worker"
    go run cmd/worker/main.go --address=0.0.0.0:50051 --runtime=fake --log-level=info --log-format=dev --auto-remove=true --containerized=false --etcd-endpoint=localhost:2379

run-local-leaf:
    @echo "Running local leaf"
    go run cmd/leaf/main.go --address=0.0.0.0:50050 --log-level=info --log-format=dev --worker-addr=127.0.0.1:50051 --etcd-endpoint=localhost:2379


############################
# Testing Stuff
############################

test tag="unit" color="false" *FLAGS:
    if [ "{{color}}" = "true" ]; then GOTEST_PALETTE="red,green" GOTEST_SKIPNOTESTS="true" gotest ./... -tags={{tag}} {{FLAGS}}; fi
    if [ "{{color}}" = "false" ]; then go test ./... -tags={{tag}} {{FLAGS}} 2>&1 | grep -v 'no test files'; fi

############################
# Misc. Stuff
############################
# Update all go deps
update-deps:
    go get -u ./...
    go mod tidy

lint *FLAGS:
    golangci-lint run {{FLAGS}}

# Start tmux log viewer
tmux-logs:
    ./test/scripts/monitor-logs.sh
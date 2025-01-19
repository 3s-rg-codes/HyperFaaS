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
    protoc --proto_path=proto "proto/controller/controller.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/leaf/leaf.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/common/common.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative

# build the worker binary
build-worker:
    go build -o bin/ cmd/workerNode/main.go

# build all go functions
build-functions-go:
    find ./functions/go/*/ -maxdepth 0 -type d | xargs -I {} bash -c 'just build-function-go $(basename "{}")'


# build a single go function
build-function-go function_name:
    # Pro Tip: you can add `--progress=plain` as argument to build, and then the full output of your build will be shown.
    docker build -t hyperfaas-{{function_name}} -f ./function.Dockerfile --build-arg FUNCTION_NAME="{{function_name}}" .

# build all
build: build-functions-go build-worker




############################
# Running Stuff
############################

# run the worker with default configurations. Make sure to run just build every time you change the code
# Alternatively, run just dev if you want to make sure you are always running the latest code
start-rebuild:
    @echo "Starting docker service"
    docker compose up --build

start:
    @echo "Starting docker service"
    docker compose up -d

logs:
    docker compose logs -f



stop:
    @echo "Stopping docker service"
    docker compose down

# generates proto, builds binary, builds docker go and runs the workser
dev: build start




############################
# Testing Stuff
############################

# run the tests
test-all:
    go test -v ./...

# run a test with a specific test name
test name:
    go test -run {{name}} ./...

############################
# Misc. Stuff
############################
# Remove all docker containers/images, and all logs
clean:
    rm -rf functions/logs/*
    docker ps -a | grep hyperfaas- | awk '{print $1}' | xargs docker rm -f
    docker images | grep hyperfaas- | awk '{print $3}' | xargs docker rmi -f
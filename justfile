# For more information, see https://github.com/casey/just

set dotenv-load
set export
set windows-shell := ["powershell"]
set shell := ["bash", "-c"]

default:
  @just --list --unsorted

# install  proto and protoc-gen-go
setup:
    @echo "Installing protoc"
    sudo apt install -y protobuf-compiler
    @echo "Installing protoc-gen-go"
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2
    export PATH="$PATH:$(go env GOPATH)/bin"

# generate the proto files
proto:
    @echo "Generating proto files"
    protoc --proto_path=proto "proto/function/function.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative
    protoc --proto_path=proto "proto/controller/controller.proto" --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative

# build the worker binary
build:
    go build -o bin/ cmd/workerNode/main.go


defaultImages := "all"
# build the docker images of the implemented functionss
buildImage image_name=defaultImages:
    go run functions/images/buildImages.go  --imagName {{image_name}}

# run the worker with default configurations
run: 
    @echo "Running the worker"
    ./bin/main -id="a" -runtime="docker" -log-level="debug" -log-handler="dev" -auto-remove="true"

# run the tests 
test test_name="*":
    go test -run {{test_name}} ./cmd/workerNode/...

# generates proto, builds binary, builds docker images and runs the workser
dev: setup proto build buildImage run

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
    go build -o bin/ cmd/worker/main.go

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
    docker compose up --detach --remove-orphans

restart:
    @echo "Restarting docker service"
    docker compose restart

stop:
    @echo "Stopping docker service"
    docker compose down
    
d:
    @echo "Starting docker service"
    docker compose up --build --detach


# generates proto, builds binary, builds docker go and runs the workser
dev: build start

run-local-database:
    @echo "Running local database"
    go run cmd/database/main.go --address=0.0.0.0:8080

run-local-worker:
    @echo "Running local worker"
    go run cmd/worker/main.go --address=0.0.0.0:50051 --runtime=fake --log-level=info --log-format=dev --auto-remove=true --containerized=false --caller-server-address=127.0.0.1:50052 --database-type=http

run-local-leaf:
    @echo "Running local leaf"
    go run cmd/leaf/main.go --address=0.0.0.0:50050 --log-level=info --log-format=dev --worker-ids=127.0.0.1:50051 --scheduler-type=mru --database-address=http://localhost:8080


############################
# Testing Stuff
############################

# run the tests
test-all:
    go test -v ./...

# run a test with a specific test name
test name:
    go test -run {{name}} ./...

#Containerized integration tests via docker compose
build-integration-containerized-all:
    ENTRYPOINT_CMD="-test_cases=all" docker compose -f test-compose.yaml up --build

build-integration-containerized list:
    ENTRYPOINT_CMD="-test_cases={{list}}" docker compose -f test-compose.yaml up --build

test-integration-containerized-all:
    ENTRYPOINT_CMD="-test_cases=all" docker compose -f test-compose.yaml up

test-integration-containerized list:
    ENTRYPOINT_CMD="-test_cases={{list}}" docker compose -f test-compose.yaml up

#Local integration tests
test-integration-local-all runtime loglevel:
    cd ./cmd/database && go run . &
    cd ./tests/worker && go run . {{runtime}} {{loglevel}}

###### Metrics Tests ########
metrics-client:
    go run ./cmd/metrics-client

metrics-test:
    go run -race ./tests/metrics/main.go

metrics-analyse:
    cd benchmarks && uv run analyse.py

############################
# Misc. Stuff
############################
# Remove all docker containers/images, and all logs
delete-logs:
    rm -rf log/*

# Print the last 100 lines of the worker log
worker-log:
    docker logs $(docker ps -a | grep worker | awk '{print $1}') --tail 100
pprof-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/goroutine

memory-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/heap
trace-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool trace http://localhost:6060/debug/pprof/trace?seconds=60
block-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/block
clean:
    rm -rf functions/logs/*
    docker ps -a | grep hyperfaas- | awk '{print $1}' | xargs docker rm -f
    docker images | grep hyperfaas- | awk '{print $3}' | xargs docker rmi -f

#Kills the locally running integration test in case it cant shutdown gracefully
kill-worker:
    pid=$(ps aux | grep '[e]xe/worker' | awk '{print $2}') && kill -9 $pid

kill-db:
    pid=$(ps aux | grep '[e]xe/database' | awk '{print $2}') && kill -9 $pid

kill: kill-worker kill-db



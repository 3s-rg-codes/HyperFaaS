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

# build the worker binary
build-worker:
    go build -o bin/ cmd/worker/main.go
leaf:
    docker compose up -d --build leaf
worker:
    docker compose up -d --build worker
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
start-rebuild service  runtime_type="docker" log_level="info":
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose up -d --no-deps --build {{service}}
alias sr := start-rebuild

start runtime_type="docker" log_level="info":
    @echo "Starting docker service"
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose up --detach --remove-orphans

# Starts 4 workers , 2 leafs and a lb
start-full runtime_type="docker" log_level="info":
    @echo "Starting docker service"
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose --file ./full-compose.yaml up  --detach --remove-orphans 
alias sfull := start-full

# Starts 4 workers , 2 leafs and a lb and rebuilds all images
start-full-rebuild runtime_type="docker" log_level="info":
    @echo "Starting docker service"
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose --file ./full-compose.yaml up  --detach --remove-orphans --build

restart-full runtime_type="docker" log_level="info":
    @echo "Restarting docker service"
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose --file ./full-compose.yaml restart

stop-full runtime_type="docker" log_level="info":
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose --file ./full-compose.yaml down

restart:
    @echo "Restarting docker service"
    RUNTIME_TYPE=docker LOG_LEVEL=info docker compose restart

stop:
    @echo "Stopping docker service"
    docker compose down
    
d runtime_type="docker" log_level="info":
    @echo "Starting docker service"
    RUNTIME_TYPE={{runtime_type}} LOG_LEVEL={{log_level}} docker compose up --build --detach


# generates proto, builds binary, builds docker go and runs the workser
dev: build start

run-local-worker:
    @echo "Running local worker"
    go run cmd/worker/main.go --address=0.0.0.0:50051 --runtime=fake --log-level=info --log-format=dev --auto-remove=true --containerized=false --etcd-endpoint=localhost:2379

run-local-leaf:
    @echo "Running local leaf"
    go run cmd/leaf/main.go --address=0.0.0.0:50050 --log-level=info --log-format=dev --worker-addr=127.0.0.1:50051 --etcd-endpoint=localhost:2379


############################
# Testing Stuff
############################

# run the tests
test-all:
    go test -v ./...

# run a test with a specific test name
test name:
    go test -run {{name}} ./...

test-unit color="false":
    if [ "{{color}}" = "true" ]; then GOTEST_PALETTE="red,green" gotest -v ./... -tags=unit; fi
    if [ "{{color}}" = "false" ]; then go test -v ./... -tags=unit; fi

test-integration color="false":
    if [ "{{color}}" = "true" ]; then GOTEST_PALETTE="red,green" gotest -v ./... -tags=integration; fi
    if [ "{{color}}" = "false" ]; then go test -v ./... -tags=integration; fi

test-e2e color="false" *FLAGS:
    if [ "{{color}}" = "true" ]; then GOTEST_PALETTE="red,green" gotest ./test -tags=e2e {{FLAGS}}; fi
    if [ "{{color}}" = "false" ]; then go test ./test -tags=e2e {{FLAGS}}; fi

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
    cd ./tests/worker && go run . {{runtime}} {{loglevel}}

###### Metrics Tests ########
metrics-client:
    go run ./cmd/metrics-client

load-test:
    go run ./test/leaf/main.go

metrics-analyse:
    cd benchmarks && uv run analyse.py

############################
# Misc. Stuff
############################
# Update all go deps
update-deps:
    go get -u ./...
    go mod tidy

# Remove all docker containers/images, and all logs
delete-logs:
    rm -rf log/*

# Print the last 100 lines of the worker log
worker-log:
    docker logs $(docker ps -a | grep worker | awk '{print $1}') --tail 100
pprof-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/goroutine
pprof-leaf:
    docker exec -it $(docker ps | grep leaf | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/goroutine

docker-logs-tail component:
    docker logs $(docker ps -a | grep {{component}} | awk '{print $1}') --tail 100
docker-logs component:
    docker logs $(docker ps -a | grep {{component}} | awk '{print $1}')

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

kill: kill-worker


lint:
    golangci-lint run

# Print the last n function logs
function-logs n:
    sudo bash -c ' cd /var/lib/docker/volumes/function-logs/_data && ls -t | head -n {{n}} | xargs -r cat'

# Start tmux log viewer
tmux-logs:
    ./test/scripts/monitor-logs.sh

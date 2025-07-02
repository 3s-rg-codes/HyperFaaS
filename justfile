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
    go run cmd/database/main.go --address=0.0.0.0:8999

run-local-worker:
    @echo "Running local worker"
    go run cmd/worker/main.go --address=localhost:50051 --runtime=docker --log-level=info --log-format=dev --auto-remove=true --containerized=false --caller-server-address=127.0.0.1:50052 --database-type=http

run-local-leaf:
    @echo "Running local leaf"
    go run cmd/leaf/main.go --address=localhost:50050 --log-level=debug --log-format=text --worker-ids=127.0.0.1:50051 --database-address=http://localhost:8999


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

load-test:
    go run ./tests/leaf/main.go

metrics-analyse:
    cd benchmarks && uv run main.py --analyse --db-path metrics.db --scenarios-path ../load-generator/generated_scenarios_run_1.json --plot-save-path ./plots/

metrics-plot:
    cd benchmarks && uv run plot.py--plot --prefix $(date +%Y-%m-%d) --plot-save-path ./plots/
metrics-process:
    cd benchmarks && uv run process.py --db-path ../benchmarks/metrics.db --active-calls-window-size 100

metrics-clean-training:
    sqlite3 benchmarks/metrics.db "drop table training_data;"
metrics-verify:
    sqlite3 benchmarks/metrics.db ".headers on" "select count(), function_instances_count from training_data group by function_instances_count limit 100;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count() , active_function_calls_count from training_data group by active_function_calls_count limit 100;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count(distinct(function_cpu_usage)) from training_data;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count(distinct(function_ram_usage)) from training_data;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count(distinct(worker_cpu_usage)) from training_data;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count(distinct(worker_ram_usage)) from training_data;"
    sqlite3 benchmarks/metrics.db ".headers on" "select count(case when function_cpu_usage = 0.0 then 1 end) as zero_count, count(case when function_cpu_usage != 0.0 then 1 end) as non_zero_count from training_data;"
    sqlite3 benchmarks/metrics.db ".headers on" "select scenario, count() from metrics where grpc_req_duration is null and error is null and timeout is null group by scenario;"

############################
# Data pipeline
############################
run-full-pipeline time="1m" total_runs="3" address="localhost:50050":
    #!/bin/bash
    # run the load generation
    just load-generator/register-functions {{address}}
    just load-generator/run-sequential {{total_runs}} {{time}} {{address}}
    # call pull metrics script : this will fail unless you have it locally
    # This script lives outside the repo - its infra specific
    ../pull_metrics.sh
    # process the metrics
    just load-generator/export-sequential

    just load-generator/process-sequential

    just load-generator/scenarios-sequential

    just load-generator/plot-sequential

    # Move the experiment run to the training data folder
    timestamp=$(date +%Y-%m-%d_%H-%M-%S)
    mkdir -p ~/training_data/${timestamp}
    mv ./benchmarks/metrics.db ~/training_data/${timestamp}/metrics.db
    mv ./load-generator/generated_scenarios_*.json ~/training_data/${timestamp}/
    mv ./benchmarks/plots ~/training_data/${timestamp}/plots

clean-metrics:
    rm ./benchmarks/metrics.db 2> /dev/null
    rm ./load-generator/generated_scenarios.json 2> /dev/null
    rm ./load-generator/test_results.csv 2> /dev/null
    rm ./load-generator/stderr_output.txt 2> /dev/null

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
pprof-leaf:
    docker exec -it $(docker ps | grep leaf | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/goroutine

docker-logs component:
    docker logs $(docker ps -a | grep {{component}} | awk '{print $1}') --tail 100
memory-worker:
    docker exec -it $(docker ps | grep worker | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/heap

memory-leaf:
    docker exec -it $(docker ps | grep leaf | awk '{print $1}') go tool pprof http://localhost:6060/debug/pprof/heap
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



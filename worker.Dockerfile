ARG GO_VERSION=1.22
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine

WORKDIR /root/

COPY . .
COPY ./cmd/worker/main.go .

RUN go mod tidy
RUN go mod download

#Copy main function
RUN GOOS=linux go build -o main main.go

CMD ["./main", "-id=my_worker_id", "-runtime=docker", "-log-level=debug", "-log-handler=dev", "auto-remove=true"]
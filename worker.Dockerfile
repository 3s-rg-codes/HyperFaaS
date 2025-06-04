ARG GO_VERSION=1.23
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine

WORKDIR /root/

COPY . .
COPY ./cmd/worker/main.go .

RUN go mod download

#Copy main function
RUN GOOS=linux go build -o main main.go

CMD ["./main", "-address=0.0.0.0:50051", "-runtime=docker", "-log-level=debug", "-log-format=text", "--auto-remove=true", "-containerized=true", "-caller-server-address=0.0.0.0:50052", "-database-type=http"]

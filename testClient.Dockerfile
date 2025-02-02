ARG GO_VERSION=1.22
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine

WORKDIR /root/

COPY . .
COPY ./tests/worker/integration.go .

RUN go mod tidy
RUN go mod download

RUN GOOS=linux go build -o main integration.go
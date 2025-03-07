ARG GO_VERSION=1.23
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine

WORKDIR /root/

COPY . .
COPY ./tests/worker/* .


RUN go mod tidy
RUN go mod download



RUN GOOS=linux go build -o main ./tests/worker/
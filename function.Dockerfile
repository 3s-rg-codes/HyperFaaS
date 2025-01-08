# This needs to run from the root directory context, as it depends on the main go.mod file (because we want to use the functionRuntimeInterface from the root of the project).
# It is in this directory so that its easier to understand what it does (building the functions in this folder), but it can't be run from here. Look at the justfile for that.
ARG GO_VERSION=1.22
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine as builder
ARG FUNCTION_NAME="echo"
WORKDIR /root/
COPY go.mod go.sum ./
RUN go mod download
# We only need the pkg and proto dirs for building. Honestly we don't even need the whole pkg/ folder, but this is easer to think about and will not break if we rename the functionRuntimeInterface package.
COPY pkg/ ./pkg/
COPY proto/function ./proto/function
# Copy only the function we want to build over
COPY functions/go/${FUNCTION_NAME} ./functions/go/${FUNCTION_NAME}
RUN go build -o handler ./functions/go/${FUNCTION_NAME}/main.go

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache --upgrade bash

WORKDIR /root/
COPY --from=builder /root/handler ./handler
COPY ./functions/go/set_env.sh .
# Copy the whole folder over as well, just in case there is some other file we need (might be an image or whatever).
COPY ./functions/go/${FUNCTION_NAME}/ ./

EXPOSE 50052
ENV CALLER_SERVER_ADDRESS="worker"

CMD ["sh", "-c" ,"source set_env.sh && echo 'ContainerId is: $CONTAINER_ID' && ./handler &&"]

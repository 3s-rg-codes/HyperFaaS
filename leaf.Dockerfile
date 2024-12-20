ARG GO_VERSION=1.22
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine as builder
WORKDIR /root/
COPY ./go.mod go.sum ./
RUN go mod download
# Copy packages needed for worker
COPY pkg/leaf ./pkg
COPY proto/leaf ./proto/
#Copy main function
COPY cmd/leaf/main.go ./cmd/main.go
RUN go build -o leaf ./cmd/main.go

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache --upgrade bash

WORKDIR /root/
COPY --from=builder /root/leaf ./leaf

#Leaf is exposed on this port
EXPOSE 50052

CMD ["./leaf"]
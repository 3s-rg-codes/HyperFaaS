ARG GO_VERSION=1.24.3
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine as builder
WORKDIR /root/
COPY ./go.mod go.sum ./
RUN go mod download
COPY . . 
RUN go build -o leaf ./cmd/leaf/main.go

FROM golang:${GO_VERSION}-alpine

RUN apk add --no-cache --upgrade bash

WORKDIR /root/
COPY --from=builder /root/leaf ./leaf

#Leaf is exposed on this port
EXPOSE 50052

CMD ["./leaf", "--address=0.0.0.0:50050", "--log-level=debug", "--log-format=dev", "--worker-ids=worker:50051", "--scheduler-type=mru"]
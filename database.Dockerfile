ARG GO_VERSION=1.23
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app

COPY . .
COPY cmd/database/main.go .

RUN go mod tidy && go mod download

RUN GOOS=linux go build -o main main.go

FROM alpine:${ALPINE_VERSION}

WORKDIR /root/

COPY --from=builder /app/main .

RUN chmod +x ./main

EXPOSE 8999

CMD ["./main"]
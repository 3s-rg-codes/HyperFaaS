ARG GO_VERSION=1.25.0
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMPONENT=worker
RUN echo "Building component: ${COMPONENT}"

RUN case "${COMPONENT}" in \
    "worker") \
        go build -o app ./cmd/worker/main.go ;; \
    "leaf") \
        go build -o app ./cmd/leaf/main.go ;; \
    "leafv2") \
        go build -o app ./cmd/leafv2/main.go ;; \
    "database") \
        go build -o app ./cmd/database/main.go ;; \
    "lb") \
        go build -o app ./cmd/lb/main.go ;; \
    *) \
        echo "Unknown component: ${COMPONENT}" && exit 1 ;; \
    esac

FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies based on component
ARG COMPONENT=worker
RUN case "${COMPONENT}" in \
    "worker"|"leaf"|"leafv2"|"lb") \
        apk add --no-cache netcat-openbsd bash ;; \
    "database") \
        apk add --no-cache wget ;; \
    esac

WORKDIR /app

# Copy the built binary
COPY --from=builder /app/app ./app
RUN chmod +x ./app

# Set default command (can be overridden in compose)
CMD ["./app"]

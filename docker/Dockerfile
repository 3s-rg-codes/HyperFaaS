# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25.5
ARG ALPINE_VERSION=latest

FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app

ENV GOMODCACHE=/go/pkg/mod
ENV GOCACHE=/root/.cache/go-build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG COMPONENT=worker
RUN echo "Building component: ${COMPONENT}"

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    case "${COMPONENT}" in \
        "worker") \
            go build -o app ./cmd/worker/main.go ;; \
        "leaf") \
            go build -o app ./cmd/leaf/main.go ;; \
        "routingcontroller") \
            go build -o app ./cmd/routing-controller/main.go ;; \
        *) \
            echo "Unknown component: ${COMPONENT}" && exit 1 ;; \
        esac

FROM golang:${GO_VERSION}-alpine

# Install runtime dependencies based on component
ARG COMPONENT=worker
RUN case "${COMPONENT}" in \
    "worker"|"leaf"|"routingcontroller") \
        apk add --no-cache netcat-openbsd bash ;; \
    esac

WORKDIR /app

# Copy the built binary
COPY --from=builder /app/app ./app
RUN chmod +x ./app

# Set default command (can be overridden in compose)
CMD ["./app"]

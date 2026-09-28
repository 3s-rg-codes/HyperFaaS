ARG GO_VERSION=1.26.3
ARG FUNCTION_PATH

FROM golang:${GO_VERSION}-alpine AS builder
ARG FUNCTION_PATH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /handler "$FUNCTION_PATH"

FROM scratch
COPY --from=builder /handler /handler
EXPOSE 50052
CMD ["/handler"]

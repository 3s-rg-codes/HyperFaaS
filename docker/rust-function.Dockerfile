FROM rust:1.87-alpine AS builder
RUN apk add --no-cache musl-dev protobuf-dev
WORKDIR /build
COPY functions/rust/runtime /runtime
COPY functions/rust/echo-http /echo-http
WORKDIR /echo-http
RUN rustup target add x86_64-unknown-linux-musl
RUN cargo build --release --target x86_64-unknown-linux-musl

FROM scratch
COPY --from=builder /echo-http/target/x86_64-unknown-linux-musl/release/hyperfaas-rust-echo-http /function
EXPOSE 50052
CMD ["/function"]

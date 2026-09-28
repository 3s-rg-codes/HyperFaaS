FROM node:22-alpine AS builder
WORKDIR /build
COPY functions/node/runtime/package.json /runtime/
COPY functions/node/runtime/index.js functions/node/runtime/ready.proto /runtime/
RUN cd /runtime && npm install --omit=dev

FROM node:22-alpine
COPY --from=builder /runtime/node_modules /runtime/node_modules
COPY functions/node/runtime/index.js functions/node/runtime/ready.proto /runtime/
COPY functions/node/echo-http/main.js /main.js
COPY functions/node/echo-http/function /function
RUN chmod +x /function
EXPOSE 50052
CMD ["/function"]

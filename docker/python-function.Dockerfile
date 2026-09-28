FROM python:3.13-alpine AS builder
RUN apk add --no-cache gcc musl-dev libffi-dev
COPY functions/python/runtime /runtime
RUN pip install --prefix=/install --no-cache-dir /runtime

FROM python:3.13-alpine
COPY --from=builder /install /usr/local
COPY functions/python/echo-http/main.py /main.py
COPY functions/python/echo-http/function /function
RUN chmod +x /function
EXPOSE 50052
CMD ["/function"]

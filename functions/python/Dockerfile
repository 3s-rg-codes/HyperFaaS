FROM python:3.12

RUN pip install grpcio grpcio-tools

WORKDIR /root

COPY pkg/ ./pkg/
COPY proto/function ./proto/function

RUN python -m grpc_tools.protoc -I./proto/function --python_out=. --grpc_python_out=. ./proto/function/*.proto

FROM python:3.12

WORKDIR /root

COPY --from=grpc-builder /root/ ./root/handler

COPY ./functions/python/set_env.sh .

COPY ./functions/python/hello/ ./

RUN pip install -r requirements.txt

EXPOSE 50052
ENV CALLER_SERVER_ADDRESS=host.docker.internal

CMD ["sh", "-c" ,"source set_env.sh && echo 'ContainerId is: $CONTAINER_ID' && ./handler"]



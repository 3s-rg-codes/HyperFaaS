FROM golang:1.22.4 as builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY *.go ./


RUN CGO_ENABLED=0 GOOS=linux go build -o /handler



FROM alpine:latest

WORKDIR /root/

COPY --from=builder /handler .

RUN mkdir logs
RUN touch logs/fn.log
RUN apk add --no-cache --upgrade bash

COPY set_env.sh .
RUN chmod +x set_env.sh


# Expose port 50052 to the outside world
EXPOSE 50052

# Command to run the executable
# CMD ["sh", "-c" ,"./set_env.sh && . /root/.env && echo $CONTAINER_ID && ./echo"]
CMD ["sh", "-c" ,"source set_env.sh && echo $CONTAINER_ID && ./handler"]

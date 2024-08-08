package fakeyschmakey

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	zerolog "github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type handler func(context.Context, *Request) (*Response, error)

type Request struct {
	Data string
	Id   string
}

type Response struct {
	Data string
	Id   string
}

type Function struct {
	timeout  int
	address  string
	request  *Request
	response *Response
	id       string
}

func New(timeout int, id string) *Function {
	return &Function{
		timeout:  timeout,
		address:  "localhost:50052",
		request:  &Request{},
		response: &Response{},
		id:       id,
	}
}

func (f *Function) Ready(handler handler) {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	fmt.Println(wd)
	functionLog := configLog(fmt.Sprintf(wd+"/functions/logs/%s-%s.log", time.Now().Format("2006-01-02-15-04-05"), f.id))
	connection, err := grpc.NewClient(f.address, grpc.WithTransportCredentials(insecure.NewCredentials()))

	functionLog.Debug().Msgf("Connected to the worker: %v", f.address)

	defer connection.Close()

	if err != nil {
		functionLog.Error().Msgf("failed to connect: %v", err)
	}

	c := pb.NewFunctionServiceClient(connection)

	//Set the id in the response to the id of the container
	f.response.Id = f.id

	defer functionLog.Info().Msgf("Closing connection.")
	first := true

	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.timeout)*time.Second)
		defer cancel()

		//We ask for a new request whilst sending the response of the previous one
		p, err := c.Ready(ctx, &pb.Payload{Data: f.response.Data, Id: f.response.Id, FirstExecution: first})
		first = false

		if err != nil {
			functionLog.Error().Msgf("failed to call: %v", err)
			return
		}
		functionLog.Debug().Msgf("Received request: %v", p.Data)

		f.request = &Request{p.Data, p.Id}

		f.response, err = handler(ctx, f.request)

		functionLog.Debug().Msgf("Function handler called and generated response: %v", f.response.Data)

		if err != nil {
			functionLog.Error().Msgf("Function failed: %v", err)
			return
		}

	}
}
func configLog(logFile string) *zerolog.Logger {
	// Open the log file

	err := os.MkdirAll(filepath.Dir(logFile), os.ModePerm)
	if err != nil {
		panic(err)
	}

	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)

	if err != nil {
		panic(err)
	}

	log := zerolog.New(file).With().Timestamp().Logger()
	return &log
}

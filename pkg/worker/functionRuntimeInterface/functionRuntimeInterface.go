package functionRuntimeInterface

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type handler func(context.Context, *Request) *Response

type Request struct {
	Data []byte
	Id   string
}

type Response struct {
	Data  []byte
	Error string
	Id    string
}

type Function struct {
	timeout    int
	address    string
	request    *Request
	response   *Response
	instanceId string
	functionId string
}

func New(timeout int) *Function {
	address, ok := os.LookupEnv("CALLER_SERVER_ADDRESS")
	if !ok {
		fmt.Printf("Environment variable CALLER_SERVER_ADDRESS not found")
	}
	functionId, ok := os.LookupEnv("FUNCTION_ID")
	if !ok {
		fmt.Printf("Environment variable FUNCTION_ID not found")
	}
	fmt.Printf("CALLER_SERVER_ADDRESS: %s", address)

	return &Function{
		timeout:    timeout,
		address:    address,
		request:    &Request{},
		response:   &Response{},
		instanceId: getID(),
		functionId: functionId,
	}
}

// Ready is called from inside the function instance container. It waits for a request from the caller server.
func (f *Function) Ready(handler handler) {
	logger := configLog(fmt.Sprintf("/logs/%s-%s.log", time.Now().Format("2006-01-02-15-04-05"), f.instanceId))

	logger.Info("Address", "address", f.address)

	connection, err := grpc.NewClient(f.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Error("failed to connect", "error", err)
	}
	defer connection.Close()

	c := pb.NewFunctionServiceClient(connection)

	//Set the id in the response to the id of the container
	f.response.Id = f.instanceId

	defer logger.Info("Closing connection.")
	first := true

	for {
		ctx, _ := context.WithTimeout(context.Background(), time.Duration(f.timeout)*time.Second)

		//We ask for a new request whilst sending the response of the previous one
		p, err := c.Ready(ctx, &pb.Payload{Data: f.response.Data, InstanceId: &common.InstanceID{Id: f.response.Id}, FunctionId: &common.FunctionID{Id: f.functionId}, FirstExecution: first, Error: &common.Error{Message: f.response.Error}})

		//cancel()

		first = false
		if err != nil {
			logger.Error("failed to call", "error", err)
			return
		}
		logger.Info("Received request", "data", p.Data)

		f.request = &Request{p.Data, p.InstanceId.Id}

		f.response = handler(ctx, f.request)

		logger.Debug("Function handler called and generated response", "response", f.response.Data)

		if err != nil {
			logger.Error("Function failed", "error", err)
			return
		}

	}

}

func configLog(logFile string) *slog.Logger {
	// Open the log file
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)

	if err != nil {
		console := slog.New(slog.NewTextHandler(os.Stdout, nil))
		console.Error("Failed to create log file, using stdout", "error", err)
		return console
	}

	return slog.New(slog.NewTextHandler(file, nil))
}

func getID() string {
	var id string
	file, err := os.Open(".env")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CONTAINER_ID=") {
			id = strings.TrimPrefix(line, "CONTAINER_ID=")
			break
		}
	}

	if err := scanner.Err(); err != nil {
		panic(err)
	}
	return id
}

package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"strings"
	"time"

	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type EchoService struct {
	pb.FunctionServiceClient
	new bool
}

var result *pb.Payload
var id string

const (
	address = "localhost:50052"
	logFile = "fn.log"
	logDir  = "logs"
)

func main() {

	l := startLogging()
	//Set the id
	setID()
	//sleep for 5 seconds to give the caller time to start
	time.Sleep(5 * time.Second)
	log.Printf("Starting the Echo function\n")
	//connect to the worker
	c, err := connectToWorker()
	log.Printf("Connected to the worker\n")

	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	for {
		callToProcess, err := c.sendCall()
		if err != nil {
			log.Fatalf("failed to send call: %v", err)
		}

		result, err = handler(callToProcess)

		if err != nil {
			log.Fatal("Function failed")
		}
	}

	l.Close()
}

func connectToWorker() (*EchoService, error) {

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))

	//defer connection.Close()
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
		return nil, err
	}

	EchoService := &EchoService{FunctionServiceClient: pb.NewFunctionServiceClient(connection), new: true}

	return EchoService, nil
}

//sendCall sends a call to the worker , for now it is just a call to the Ready function
//because we check the new flag to see if we need to call the handler or not

func (es *EchoService) sendCall() (*pb.Call, error) {
	//Set the context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if es.new {

		es.new = false
		log.Printf("Calling Ready function, asking for a Call with my ID %v \n", id)
		response, err := es.Ready(ctx, &pb.Payload{Id: id})

		if err != nil {
			log.Fatalf("failed to call: %v", err)
			return nil, err
		}

		log.Printf("Called Ready function\n")
		return response, nil

	} else {

		log.Printf("Calling Ready function, asking for a Call with my ID %v and delivering payload: %s\n", id, result.Data)
		response, err := es.Ready(ctx, &pb.Payload{Data: result.Data, Id: id})

		if err != nil {
			log.Fatalf("failed to call: %v", err)
			return nil, err
		}

		log.Printf("Called Ready function\n")
		return response, nil
	}
}

func startLogging() *os.File {
	// Open the log file
	file, err := os.OpenFile(logDir+"/"+logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	log.SetOutput(file)
	return file
}

func setID() {
	file, err := os.Open(".env")
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}
	log.Printf("Container ID: %s\n", id)
}

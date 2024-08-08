package mockRuntime

import (
	"context"
	"errors"
	fakeFunctions "github.com/3s-rg-codes/HyperFaaS/cmd/workerNode/functions"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	"github.com/3s-rg-codes/HyperFaaS/pkg/functionRuntimeInterface/mock"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	uuid2 "github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"time"
)

var mutex = sync.RWMutex{}

type FakeRuntime struct {
	cr.ContainerRuntime
	simulatedLatency time.Duration
	instanceMap      map[string]string
}

func NewFakeRuntime(duration time.Duration) *FakeRuntime {
	return &FakeRuntime{simulatedLatency: duration * time.Second, instanceMap: make(map[string]string)}
}

func (fR *FakeRuntime) ContainerExists(instanceId string) bool {
	mutex.Lock()
	_, ok := fR.instanceMap[instanceId]
	mutex.Unlock()
	return ok
}

func (fR *FakeRuntime) Start(ctx context.Context, imageTag string, config *pb.Config) (string, error) {
	time.Sleep(fR.simulatedLatency)
	uuid := uuid2.New().String()
	switch imageTag {
	case "hyperfaas-crash:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "crash"
		mutex.Unlock()
		go fakeFunction("crash", uuid)
	case "hyperfaas-sleep:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "sleep"
		mutex.Unlock()
		go fakeFunction("sleep", uuid)
	case "hyperfaas-echo:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "echo"
		mutex.Unlock()
		go fakeFunction("echo", uuid)
	case "hyperfaas-hello:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "hello"
		mutex.Unlock()
		go fakeFunction("hello", uuid)
	default:
		return "", errors.New("no such image")
	}
	return uuid, nil
}

func (fR *FakeRuntime) Call(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {
	/*
		if _, ok := fR.instanceMap[req.InstanceId.Id]; !ok {
			return nil, status.Errorf(codes.NotFound, "no such container")
		}
		switch fR.instanceMap[req.InstanceId.Id] {
		case "crash": //crash the container
			panic("crash")
		case "sleep": //sleep for 2 seconds
			time.Sleep(2 * time.Second)
			return &pb.Response{Data: "slept for 2 seconds"}, nil
		case "echo": //echo the input
			return &pb.Response{Data: req.Params.Data}, nil
		case "hello": //return hello
			return &pb.Response{Data: "Hello World"}, nil
		}

	*/
	return &pb.Response{}, nil
}

func (fR *FakeRuntime) Stop(ctx context.Context, req *pb.InstanceID) (*pb.InstanceID, error) {
	mutex.Lock()
	_, ok := fR.instanceMap[req.Id]
	mutex.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no such container")
	}
	mutex.Lock()
	delete(fR.instanceMap, req.Id)
	mutex.Unlock()
	return req, nil
}

func (fR *FakeRuntime) Status(req *pb.StatusRequest, stream pb.Controller_StatusServer) error {

	return nil
}

func (fR *FakeRuntime) NotifyCrash(ctx context.Context, instanceId string) error {
	ch := make(chan struct{})
	<-ch //blocks forever
	return nil
}

func fakeFunction(functionType string, id string) {
	f := mock.New(120, id)

	switch functionType {
	case "crash":
		f.Ready(fakeFunctions.HandlerCrash)
	case "sleep":
		f.Ready(fakeFunctions.HandlerSleep)
	case "echo":
		f.Ready(fakeFunctions.HandlerEcho)
	case "hello":
		f.Ready(fakeFunctions.HandlerHello)
	}
}

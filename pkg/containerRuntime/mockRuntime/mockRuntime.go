package mockRuntime

import (
	"context"
	"errors"
	"fmt"
	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	uuid2 "github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strconv"
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
	uuid := uuid2.New().String()
	switch imageTag {
	case "hyperfaas-crash:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "crash"
		mutex.Unlock()
	case "hyperfaas-sleep:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "sleep"
		mutex.Unlock()
	case "hyperfaas-echo:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "echo"
		mutex.Unlock()
	case "hyperfaas-hello:latest":
		mutex.Lock()
		fR.instanceMap[uuid] = "hello"
		mutex.Unlock()
	default:
		return "", errors.New("no such image")
	}
	return uuid, nil
}

func (fR *FakeRuntime) ContainerCall(ctx context.Context, req *pb.CallRequest) (*pb.Response, error) {
	log.Debug().Msgf("Passing call with payload: %v to channel of instance ID %s", req.Params.Data, req.InstanceId.Id)

	if !fR.ContainerExists(req.InstanceId.Id) {
		err := fmt.Errorf("instance ID %s does not exist", req.InstanceId.Id)
		log.Error().Err(err).Msgf("Error passing call with payload: %v", req.Params.Data)
		return nil, status.Errorf(codes.NotFound, err.Error())
	}

	switch fR.instanceMap[req.InstanceId.Id] {
	case "echo":
		return &pb.Response{Data: req.Params.Data}, nil
	case "hello":
		return &pb.Response{Data: "HELLO WORLD!"}, nil
	case "sleep":
		sleepTime, err := strconv.Atoi(req.Params.Data)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid argument")
		}
		time.Sleep(time.Duration(sleepTime) * time.Second)
		return &pb.Response{}, nil
	case "crash":
		return nil, status.Errorf(codes.Internal, "crash")
	}

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

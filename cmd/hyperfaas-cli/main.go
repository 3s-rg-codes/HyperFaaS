package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	lbpb "github.com/3s-rg-codes/HyperFaaS/proto/lb"
	leafpb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var dataFlag = &cli.StringFlag{
	Name:    "data",
	Usage:   "data to be passed to the function",
	Value:   "",
	Aliases: []string{"d"},
}

var timeoutFlag = &cli.DurationFlag{
	Name:    "timeout",
	Usage:   "example: 30s, 1m, 1h",
	Aliases: []string{"t"},
	Value:   30 * time.Second,
}

func main() {
	cmd := &cli.Command{
		Name:  "hyperfaas-cli",
		Usage: "talk to the HyperFaaS API",
		Commands: []*cli.Command{
			{
				Name:    "worker",
				Aliases: []string{"w"},
				Usage:   "talk to the worker API",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "address",
						Value: "localhost:50051",
						Usage: "address of the worker",
					},
				},
				Commands: []*cli.Command{
					{
						Name:      "start",
						Usage:     "start a function",
						ArgsUsage: "function ID",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							timeout := cmd.Duration("timeout")

							client, _, err := createWorkerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							id, err := StartFunction(client, funcID, timeout)
							if err != nil {
								return err
							}
							fmt.Printf("%v\n", id)
							return nil
						},
					},
					{
						Name:      "call",
						Usage:     "call a function",
						ArgsUsage: "function ID",
						Flags: []cli.Flag{
							dataFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							data := []byte(cmd.String("data"))
							timeout := cmd.Duration("timeout")

							client, _, err := createWorkerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							response, err := CallFunction(client, funcID, data, timeout)
							if err != nil {
								return err
							}
							fmt.Printf("%v\n", string(response))
							return nil
						},
					},
					{
						Name:      "stop",
						Usage:     "stop a function",
						ArgsUsage: "function ID",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							timeout := cmd.Duration("timeout")

							client, _, err := createWorkerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							id, err := StopInstance(client, funcID, timeout)
							if err != nil {
								return err
							}
							fmt.Printf("%v\n", id)
							return nil
						},
					},
					{
						Name:      "status",
						Usage:     "read the status update stream",
						ArgsUsage: "node ID",
						Flags: []cli.Flag{
							&cli.DurationFlag{
								Name:  "duration",
								Usage: "duration of the stream",
								Value: 10 * time.Second,
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							duration := cmd.Duration("duration")
							timeout := cmd.Duration("timeout")

							client, _, err := createWorkerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							err = ReadStatusUpdateStream(client, funcID, duration, timeout)
							if err != nil {
								return err
							}
							return nil
						},
					},
					{
						Name:      "metrics",
						Usage:     "get resource usage metrics",
						ArgsUsage: "node ID",

						Action: func(ctx context.Context, cmd *cli.Command) error {
							timeout := cmd.Duration("timeout")
							nodeID := cmd.Args().Get(0)
							client, _, err := createWorkerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							err = GetWorkerMetrics(client, nodeID, timeout)
							if err != nil {
								return err
							}
							return nil
						},
					},
				},
			},
			{
				Name:    "leaf",
				Usage:   "talk to the leaf API",
				Aliases: []string{"lf"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "address",
						Value: "localhost:50050",
					},
				},
				Commands: []*cli.Command{
					{
						Name:      "create",
						Usage:     "create a function. returns the function ID.",
						ArgsUsage: "image tag",
						Flags: []cli.Flag{
							&cli.Int64Flag{
								Name:  "cpu-period",
								Usage: "cpu config period for the function",
								Value: 100000,
							},
							&cli.Int64Flag{
								Name:  "cpu-quota",
								Usage: "cpu config quota for the function",
								Value: 50000,
							},
							&cli.Int64Flag{
								Name:  "memory",
								Usage: "memory config for the function",
								Value: 1024 * 1024 * 256, // 256MB
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							imageTag := cmd.Args().Get(0)
							cpuPeriod := cmd.Int64("cpu-period")
							cpuQuota := cmd.Int64("cpu-quota")
							memory := cmd.Int64("memory")
							timeout := cmd.Duration("timeout")

							client, _, err := createLoadBalancerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							if _, err := CreateFunction(client, imageTag, cpuPeriod, cpuQuota, memory, timeout); err != nil {
								return err
							}
							return nil
						},
					},
					{
						Name:      "call",
						Usage:     "call a function",
						ArgsUsage: "function ID",
						Flags: []cli.Flag{
							dataFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							data := []byte(cmd.String("data"))
							timeout := cmd.Duration("timeout")

							client, _, err := createLeafClient(cmd.String("address"))
							if err != nil {
								return err
							}
							response, err := ScheduleCall(client, funcID, data, timeout)
							if err != nil {
								return err
							}
							fmt.Printf("%v\n", string(response))
							return nil
						},
					},
				},
			},
			{
				Name:    "loadbalancer",
				Usage:   "talk to the loadbalancer API",
				Aliases: []string{"lb"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "address",
						Value: "localhost:50052",
					},
				},
				Commands: []*cli.Command{
					{
						Name:      "call",
						Usage:     "call a function",
						ArgsUsage: "function ID",
						Flags: []cli.Flag{
							dataFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.Args().Get(0)
							data := []byte(cmd.String("data"))
							timeout := cmd.Duration("timeout")

							client, _, err := createLoadBalancerClient(cmd.String("address"))
							if err != nil {
								return err
							}
							response, err := ScheduleCall(client, funcID, data, timeout)
							if err != nil {
								return err
							}
							fmt.Printf("%v\n", string(response))
							return nil
						},
					},
				},
			},
		},
		// all sub commands inherit timeout flag
		Flags: []cli.Flag{
			timeoutFlag,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// CreateFunction creates a function by talking to the leaf API. Returns the function ID.
func CreateFunction(client lbpb.LBClient,
	imageTag string,
	cpuPeriod int64,
	cpuQuota int64,
	memory int64,
	timeout time.Duration,
) (string, error) {
	cpuConfig := &commonpb.CPUConfig{
		Period: cpuPeriod,
		Quota:  cpuQuota,
	}

	config := &commonpb.Config{
		Memory: memory,
		Cpu:    cpuConfig,
	}

	funcReq := &commonpb.CreateFunctionRequest{
		Image: &commonpb.Image{
			Tag: imageTag,
		},
		Config: config,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	funcResp, err := client.CreateFunction(ctx, funcReq)
	if err != nil {
		fmt.Printf("Error creating function: %v\n", err)
		return "", err
	}
	fmt.Printf("Created function from imageTag '%v'. Function id: %v\n", imageTag, funcResp.FunctionId)
	return funcResp.FunctionId, nil
}

// ScheduleCall schedules a call to a function by talking either to the leaf or load balancer API. Returns the response data.
func ScheduleCall(client HyperFaaSClient,
	funcID string,
	data []byte,
	timeout time.Duration,
) ([]byte, error) {
	scheduleCallReq := &commonpb.CallRequest{
		FunctionId: funcID,
		Data:       data,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	scheduleCallResponse, err := client.ScheduleCall(ctx, scheduleCallReq)
	if err != nil {
		fmt.Printf("Error scheduling call: %v", err)
		return nil, err
	}
	if scheduleCallResponse.Error != nil {
		fmt.Printf("Internal error scheduling call: %v\n", scheduleCallResponse.Error)
		return nil, errors.New(scheduleCallResponse.Error.Message)
	}
	return scheduleCallResponse.Data, nil
}

// StartFunction starts a function by talking to the worker API. Returns the instance ID.
func StartFunction(client workerpb.WorkerClient,
	funcID string,
	timeout time.Duration,
) (string, error) {
	startReq := &workerpb.StartRequest{
		FunctionId: funcID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	instanceID, err := client.Start(ctx, startReq)
	if err != nil {
		fmt.Printf("Error starting function: %v", err)
		return "", err
	}
	fmt.Printf("Started instance of function with instance id: %v\n", instanceID.InstanceId)
	return instanceID.InstanceId, nil
}

// Call Function sends a call to the worker directly. It returns the response data.
func CallFunction(client workerpb.WorkerClient,
	funcID string,
	data []byte,
	timeout time.Duration,
) ([]byte, error) {
	callReq := &commonpb.CallRequest{
		FunctionId: funcID,
		Data:       data,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	callResp, err := client.Call(ctx, callReq)
	if err != nil {
		fmt.Printf("Error calling function: %v", err)
		return nil, err
	}
	return callResp.Data, nil
}

// StopInstance stops an instance of a function by talking to the worker API. Returns the instance ID.
func StopInstance(client workerpb.WorkerClient,
	id string,
	timeout time.Duration,
) (string, error) {
	stopReq := &workerpb.StopRequest{
		InstanceId: id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	respInstId, err := client.Stop(ctx, stopReq)
	if err != nil {
		fmt.Printf("Error stopping instance: %v", err)
		return "", err
	}
	return respInstId.InstanceId, nil
}

// ReadStatusUpdateStream reads the status update stream from the worker API for a duration and prints the status updates.
func ReadStatusUpdateStream(client workerpb.WorkerClient,
	nodeId string,
	duration time.Duration,
	timeout time.Duration,
) error {
	statusReq := &workerpb.StatusRequest{
		NodeId: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stream, err := client.Status(ctx, statusReq)
	if err != nil {
		fmt.Printf("Error getting status: %v\n", err)
		return err
	}
	for {
		select {
		case <-time.After(duration):
			return nil
		case <-ctx.Done():
			return nil
		default:
			statusUpdate, err := stream.Recv()
			if err != nil {
				fmt.Printf("Error getting status: %v\n", err)
				return err
			}
			fmt.Printf("Status update: %#v\n", statusUpdate)
		}
	}
}

// GetWorkerMetrics gets the metrics from the worker API and prints them.
func GetWorkerMetrics(client workerpb.WorkerClient,
	nodeId string,
	timeout time.Duration,
) error {
	metricsReq := &workerpb.MetricsRequest{
		NodeId: nodeId,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r, err := client.Metrics(ctx, metricsReq)
	if err != nil {
		fmt.Printf("Error getting metrics: %v\n", err)
		return err
	}
	fmt.Printf("CPU usage: %v%%\n", r.CpuPercentPercpus)
	fmt.Printf("RAM usage: %v%%\n", r.UsedRamPercent)
	return nil
}

func createWorkerClient(address string) (workerpb.WorkerClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return workerpb.NewWorkerClient(conn), conn, nil
}

func createLeafClient(address string) (leafpb.LeafClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return leafpb.NewLeafClient(conn), conn, nil
}

func createLoadBalancerClient(address string) (lbpb.LBClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return lbpb.NewLBClient(conn), conn, nil
}

type HyperFaaSClient interface {
	ScheduleCall(context.Context, *commonpb.CallRequest, ...grpc.CallOption) (*commonpb.CallResponse, error)
}

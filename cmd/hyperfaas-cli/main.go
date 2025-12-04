package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/3s-rg-codes/HyperFaaS/pkg/metadata"
	commonpb "github.com/3s-rg-codes/HyperFaaS/proto/common"
	workerpb "github.com/3s-rg-codes/HyperFaaS/proto/worker"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var dataFlag = &cli.StringFlag{
	Name: "data",
	Usage: `data to be passed to the function.
	JSON object that matches the proto request:
		- use normal JSON values for numbers/strings/bools,
		- nested objects for nested messages,
		- arrays for repeated fields,
		- and base64-encoded strings for bytes fields.`,
	Value:   "",
	Aliases: []string{"d"},
}

var globalTimeoutFlag = &cli.DurationFlag{
	Name:    "timeout",
	Usage:   "example: 30s, 1m, 1h",
	Aliases: []string{"t"},
	Value:   30 * time.Second,
}

var etcdEndpointsFlag = &cli.StringSliceFlag{
	Name:  "etcd-endpoint",
	Usage: "Etcd endpoint (repeatable)",
	Value: []string{"localhost:2379"},
}

var metadataPrefixFlag = &cli.StringFlag{
	Name:  "metadata-prefix",
	Usage: "Etcd key prefix for function metadata",
	Value: metadata.DefaultPrefix,
}

var metadataDialTimeoutFlag = &cli.DurationFlag{
	Name:  "metadata-dial-timeout",
	Usage: "Dial timeout when connecting to etcd",
	Value: metadata.DefaultDialTimeout,
}

var protoFileFlag = &cli.StringFlag{
	Name:     "proto",
	Usage:    "Path to the proto file defining the service",
	Required: true,
}

var protoImportPathFlag = &cli.StringSliceFlag{
	Name:  "proto-path",
	Usage: "Additional import paths for proto dependencies (repeatable)",
	Value: []string{},
}

var proxyMethodFlag = &cli.StringFlag{
	Name:     "method",
	Usage:    "Fully qualified gRPC method, e.g. package.Service/Method",
	Required: true,
}

var proxyFunctionFlag = &cli.StringFlag{
	Name:     "function-id",
	Usage:    "Function ID to invoke",
	Required: true,
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
				Name:  "function",
				Usage: "manage function metadata in etcd",
				Commands: []*cli.Command{
					{
						Name:      "create",
						Usage:     "create function metadata from an image tag",
						ArgsUsage: "image tag",
						Flags: []cli.Flag{
							cpuPeriodFlag,
							cpuQuotaFlag,
							memoryFlag,
							maxConcurrencyFlag,
							functionTimeoutFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							imageTag := cmd.Args().Get(0)
							if imageTag == "" {
								return errors.New("image tag is required")
							}

							metaClient, err := createMetadataClient(cmd)
							if err != nil {
								return err
							}
							defer func() {
								if err := metaClient.Close(); err != nil {
									log.Printf("warning: failed to close metadata client: %v", err)
								}
							}()

							req, err := buildFunctionRequest(cmd, imageTag)
							if err != nil {
								return err
							}

							opTimeout := cmd.Duration("timeout")
							if opTimeout <= 0 {
								opTimeout = 30 * time.Second
							}
							opCtx, cancel := context.WithTimeout(ctx, opTimeout)
							defer cancel()

							id, err := metaClient.PutFunction(opCtx, req)
							if err != nil {
								return err
							}
							fmt.Printf("Created function metadata. Function id: %s\n", id)
							return nil
						},
					},
					{
						Name:      "update",
						Usage:     "update function metadata",
						ArgsUsage: "function ID",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "image-tag",
								Usage: "new image tag (optional)",
							},
							cpuPeriodFlag,
							cpuQuotaFlag,
							memoryFlag,
							maxConcurrencyFlag,
							functionTimeoutFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							functionID := cmd.Args().Get(0)
							if functionID == "" {
								return errors.New("function ID is required")
							}

							metaClient, err := createMetadataClient(cmd)
							if err != nil {
								return err
							}
							defer func() {
								if err := metaClient.Close(); err != nil {
									log.Printf("warning: failed to close metadata client: %v", err)
								}
							}()

							opTimeout := cmd.Duration("timeout")
							if opTimeout <= 0 {
								opTimeout = 30 * time.Second
							}
							opCtx, cancel := context.WithTimeout(ctx, opTimeout)
							defer cancel()

							req, err := buildUpdateRequest(cmd, metaClient, opCtx, functionID)
							if err != nil {
								return err
							}

							if err := metaClient.PutFunctionWithID(opCtx, functionID, req); err != nil {
								return err
							}

							fmt.Printf("Updated function metadata for %s\n", functionID)
							return nil
						},
					},
					{
						Name:      "delete",
						Usage:     "delete function metadata",
						ArgsUsage: "function ID",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							functionID := cmd.Args().Get(0)
							if functionID == "" {
								return errors.New("function ID is required")
							}

							metaClient, err := createMetadataClient(cmd)
							if err != nil {
								return err
							}
							defer func() {
								if err := metaClient.Close(); err != nil {
									log.Printf("warning: failed to close metadata client: %v", err)
								}
							}()

							opTimeout := cmd.Duration("timeout")
							if opTimeout <= 0 {
								opTimeout = 30 * time.Second
							}
							opCtx, cancel := context.WithTimeout(ctx, opTimeout)
							defer cancel()

							if err := metaClient.DeleteFunction(opCtx, functionID); err != nil {
								return err
							}
							fmt.Printf("Deleted function metadata for %s\n", functionID)
							return nil
						},
					},
				},
			},
			{
				Name:  "proxy",
				Usage: "call a function through the gRPC proxy",
				Commands: []*cli.Command{
					{
						Name:  "call",
						Usage: "invoke a gRPC method on the target function",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "address",
								Value: "localhost:50053",
								Usage: "leaf gRPC proxy address",
							},
							protoFileFlag,
							protoImportPathFlag,
							proxyMethodFlag,
							proxyFunctionFlag,
							dataFlag,
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							funcID := cmd.String("function-id")
							method := cmd.String("method")
							protoFile := cmd.String("proto")
							dataJSON := cmd.String("data")
							if dataJSON == "" {
								dataJSON = "{}"
							}
							timeout := cmd.Duration("timeout")
							if timeout <= 0 {
								timeout = 30 * time.Second
							}

							methodDesc, err := loadMethodDescriptor(protoFile, cmd.StringSlice("proto-path"), method)
							if err != nil {
								return err
							}

							reqMsg := dynamicpb.NewMessage(methodDesc.Input())
							if err := protojson.Unmarshal([]byte(dataJSON), reqMsg); err != nil {
								return fmt.Errorf("failed to parse --data JSON: %w", err)
							}

							respMsg, err := invokeProxyMethod(ctx, cmd.String("address"), funcID, methodDesc, reqMsg, timeout)
							if err != nil {
								return err
							}

							out, err := protojson.MarshalOptions{
								Multiline: true,
								Indent:    "  ",
							}.Marshal(respMsg)
							if err != nil {
								return fmt.Errorf("failed to marshal response: %w", err)
							}
							fmt.Println(string(out))
							return nil
						},
					},
				},
			},
		},
		// all sub commands inherit timeout flag
		Flags: []cli.Flag{
			globalTimeoutFlag,
			etcdEndpointsFlag,
			metadataPrefixFlag,
			metadataDialTimeoutFlag,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
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

func loadMethodDescriptor(protoFile string, importPaths []string, fullMethod string) (protoreflect.MethodDescriptor, error) {
	if protoFile == "" {
		return nil, errors.New("proto file is required")
	}
	if fullMethod == "" {
		return nil, errors.New("method is required")
	}

	serviceName, methodName, err := splitMethodName(fullMethod)
	if err != nil {
		return nil, err
	}

	absProtoFile, err := filepath.Abs(protoFile)
	if err != nil {
		return nil, fmt.Errorf("resolve proto path: %w", err)
	}

	searchPaths := normalizeImportPaths(filepath.Dir(absProtoFile), importPaths)
	if len(searchPaths) == 0 {
		searchPaths = []string{"."}
	}

	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: searchPaths,
		}),
	}

	fileName := filepath.Base(absProtoFile)
	files, err := compiler.Compile(context.Background(), fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to compile proto: %w", err)
	}

	targetService := protoreflect.FullName(serviceName)
	targetMethod := protoreflect.Name(methodName)

	var descriptors []protoreflect.FileDescriptor
	for _, f := range files {
		descriptors = append(descriptors, f)
	}

	methodDesc := lookupMethodDescriptor(descriptors, targetService, targetMethod)
	if methodDesc == nil {
		return nil, fmt.Errorf("method %s not found in %s (or its imports)", fullMethod, protoFile)
	}
	return methodDesc, nil
}

func splitMethodName(fullMethod string) (string, string, error) {
	fullMethod = strings.TrimPrefix(fullMethod, "/")
	parts := strings.Split(fullMethod, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("method must be in the form package.Service/Method, got %q", fullMethod)
	}
	return parts[0], parts[1], nil
}

func normalizeImportPaths(protoDir string, extra []string) []string {
	pathSet := make(map[string]struct{})
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if _, ok := pathSet[p]; !ok {
			pathSet[p] = struct{}{}
		}
	}

	add(protoDir)
	for _, p := range extra {
		add(p)
	}
	if len(pathSet) == 0 {
		add(".")
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func lookupMethodDescriptor(files []protoreflect.FileDescriptor, service protoreflect.FullName, method protoreflect.Name) protoreflect.MethodDescriptor {
	visited := make(map[string]bool)
	for _, fd := range files {
		if m := searchMethodInFile(fd, service, method, visited); m != nil {
			return m
		}
	}
	return nil
}

func searchMethodInFile(fd protoreflect.FileDescriptor, service protoreflect.FullName, method protoreflect.Name, visited map[string]bool) protoreflect.MethodDescriptor {
	if fd == nil {
		return nil
	}
	if visited[fd.Path()] {
		return nil
	}
	visited[fd.Path()] = true

	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		svc := services.Get(i)
		if svc.FullName() == service {
			if m := svc.Methods().ByName(method); m != nil {
				return m
			}
		}
	}

	imports := fd.Imports()
	for i := 0; i < imports.Len(); i++ {
		if m := searchMethodInFile(imports.Get(i).FileDescriptor, service, method, visited); m != nil {
			return m
		}
	}
	return nil
}

func invokeProxyMethod(
	ctx context.Context,
	address string,
	functionID string,
	methodDesc protoreflect.MethodDescriptor,
	req proto.Message,
	timeout time.Duration,
) (proto.Message, error) {
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority(functionID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial proxy: %w", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("warning: failed to close connection: %v", cerr)
		}
	}()

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	serviceDesc, ok := methodDesc.Parent().(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, errors.New("method descriptor missing parent service information")
	}
	fullMethodName := fmt.Sprintf("/%s/%s", serviceDesc.FullName(), methodDesc.Name())
	resp := dynamicpb.NewMessage(methodDesc.Output())
	if err := conn.Invoke(callCtx, fullMethodName, req, resp); err != nil {
		return nil, fmt.Errorf("rpc call failed: %w", err)
	}
	return resp, nil
}

func createWorkerClient(address string) (workerpb.WorkerClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return workerpb.NewWorkerClient(conn), conn, nil
}

func createMetadataClient(cmd *cli.Command) (*metadata.EtcdClient, error) {
	endpoints := cmd.StringSlice("etcd-endpoint")
	if len(endpoints) == 0 {
		endpoints = []string{"localhost:2379"}
	}

	opts := metadata.Options{
		Prefix:      cmd.String("metadata-prefix"),
		DialTimeout: cmd.Duration("metadata-dial-timeout"),
	}

	return metadata.NewClient(endpoints, opts, nil)
}

func buildFunctionRequest(cmd *cli.Command, imageTag string) (*commonpb.CreateFunctionRequest, error) {
	if imageTag == "" {
		return nil, errors.New("image tag must not be empty")
	}

	cpuPeriod := cmd.Int64("cpu-period")
	cpuQuota := cmd.Int64("cpu-quota")
	if cpuPeriod <= 0 || cpuQuota <= 0 {
		return nil, errors.New("cpu-period and cpu-quota must be positive")
	}

	memory := cmd.Int64("memory")
	if memory <= 0 {
		return nil, errors.New("memory must be positive")
	}

	maxConcurrency := cmd.Int64("max-concurrency")
	if maxConcurrency <= 0 {
		return nil, errors.New("max-concurrency must be positive")
	}
	if maxConcurrency > math.MaxInt32 {
		return nil, fmt.Errorf("max-concurrency exceeds limit (%d)", math.MaxInt32)
	}

	d := cmd.Duration("function-timeout")
	timeoutSeconds := durationToSeconds(d)
	if timeoutSeconds <= 0 {
		return nil, errors.New("function-timeout must be greater than 0")
	}

	return &commonpb.CreateFunctionRequest{
		Image: &commonpb.Image{Tag: imageTag},
		Config: &commonpb.Config{
			Memory: memory,
			Cpu: &commonpb.CPUConfig{
				Period: cpuPeriod,
				Quota:  cpuQuota,
			},
			MaxConcurrency: int32(maxConcurrency),
			Timeout:        timeoutSeconds,
		},
	}, nil
}

func buildUpdateRequest(cmd *cli.Command, client *metadata.EtcdClient, ctx context.Context, functionID string) (*commonpb.CreateFunctionRequest, error) {
	meta, err := client.GetFunction(ctx, functionID)
	if err != nil {
		return nil, err
	}

	req := metadataToCreateRequest(meta)
	if req.Image == nil {
		req.Image = &commonpb.Image{}
	}
	if req.Config == nil {
		req.Config = &commonpb.Config{}
	}
	if req.Config.Cpu == nil {
		req.Config.Cpu = &commonpb.CPUConfig{}
	}

	if cmd.IsSet("image-tag") {
		newTag := cmd.String("image-tag")
		if newTag == "" {
			return nil, errors.New("image-tag cannot be empty")
		}
		req.Image.Tag = newTag
	}

	if cmd.IsSet("cpu-period") {
		value := cmd.Int64("cpu-period")
		if value <= 0 {
			return nil, errors.New("cpu-period must be positive")
		}
		req.Config.Cpu.Period = value
	}

	if cmd.IsSet("cpu-quota") {
		value := cmd.Int64("cpu-quota")
		if value <= 0 {
			return nil, errors.New("cpu-quota must be positive")
		}
		req.Config.Cpu.Quota = value
	}

	if cmd.IsSet("memory") {
		value := cmd.Int64("memory")
		if value <= 0 {
			return nil, errors.New("memory must be positive")
		}
		req.Config.Memory = value
	}

	if cmd.IsSet("max-concurrency") {
		mc := cmd.Int64("max-concurrency")
		if mc <= 0 {
			return nil, errors.New("max-concurrency must be positive")
		}
		if mc > math.MaxInt32 {
			return nil, fmt.Errorf("max-concurrency exceeds limit (%d)", math.MaxInt32)
		}
		req.Config.MaxConcurrency = int32(mc)
	}

	if cmd.IsSet("function-timeout") {
		dur := cmd.Duration("function-timeout")
		seconds := durationToSeconds(dur)
		if seconds <= 0 {
			return nil, errors.New("function-timeout must be greater than 0")
		}
		req.Config.Timeout = seconds
	}

	return req, nil
}

func metadataToCreateRequest(meta *metadata.FunctionMetadata) *commonpb.CreateFunctionRequest {
	req := &commonpb.CreateFunctionRequest{
		Image:  &commonpb.Image{},
		Config: &commonpb.Config{Cpu: &commonpb.CPUConfig{}},
	}

	if meta == nil {
		return req
	}

	if meta.Image != nil {
		req.Image.Tag = meta.Image.GetTag()
	}

	if meta.Config != nil {
		req.Config.Memory = meta.Config.GetMemory()
		req.Config.MaxConcurrency = meta.Config.GetMaxConcurrency()
		req.Config.Timeout = meta.Config.GetTimeout()
		if meta.Config.GetCpu() != nil {
			req.Config.Cpu.Period = meta.Config.GetCpu().GetPeriod()
			req.Config.Cpu.Quota = meta.Config.GetCpu().GetQuota()
		}
	}

	return req
}

func durationToSeconds(d time.Duration) int32 {
	if d <= 0 {
		return 0
	}
	return int32(d / time.Second)
}

var functionTimeoutFlag = &cli.DurationFlag{
	Name:  "function-timeout",
	Usage: "Execution timeout for the function (e.g. 30s, 2m)",
	Value: 30 * time.Second,
}

var cpuPeriodFlag = &cli.Int64Flag{
	Name:  "cpu-period",
	Usage: "CPU period for the function",
	Value: 100000,
}

var cpuQuotaFlag = &cli.Int64Flag{
	Name:  "cpu-quota",
	Usage: "CPU quota for the function",
	Value: 50000,
}

var memoryFlag = &cli.Int64Flag{
	Name:  "memory",
	Usage: "Memory limit in bytes",
	Value: 256 * 1024 * 1024,
}

var maxConcurrencyFlag = &cli.Int64Flag{
	Name:  "max-concurrency",
	Usage: "Maximum concurrent requests per function",
	Value: 500,
}

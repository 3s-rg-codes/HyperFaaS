package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/containerRuntime"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"log/slog"
)

func TestContainerConfig(runtime containerRuntime.ContainerRuntime, client pb.ControllerClient, logger slog.Logger, config TestConfig) error {

	cID, err := client.Start(context.Background(), &pb.StartRequest{ImageTag: &pb.ImageTag{Tag: "hyperfaas-hello:latest"}, Config: &pb.Config{Cpu: &pb.CPUConfig{Period: int64(config.CPUPeriod), Quota: int64(config.CPUQuota)}, Memory: int64(config.MemoryLimit * 1024 * 1024)}})
	if err != nil {
		logger.Error("Error occurred starting container")
		return err
	}

	stBody := runtime.ContainerStats(context.Background(), cID.Id)

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(stBody)
	if err != nil {
		logger.Error("Error reading response bytes", "error", err)
		return err
	}

	var cStats ContainerStats
	err = json.Unmarshal(buf.Bytes(), &cStats)
	if err != nil {
		logger.Error("error unmarshaling JSON", "error", err)
		return err
	}
	// Print the relevant values
	logger.Debug("Configured Memory Limit:", "bytes", cStats.MemoryStats.Limit)
	logger.Debug("Configured CPU Periods", "periods", cStats.CPUStats.ThrottlingData.Periods)
	logger.Debug("Configured CPU Quota (Throttled Time)", "quota", cStats.CPUStats.ThrottlingData.ThrottledTime)
	//t.Logf("Container inspect: %v , %v , %v", a, a.Config, a.NetworkSettings)

	_, err = client.Stop(context.Background(), cID)
	if err != nil {
		logger.Error("Error stopping container", "error", err)
		return err
	}

	return nil
}

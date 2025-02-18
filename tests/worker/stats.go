package main

import (
	"fmt"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/stats"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/3s-rg-codes/HyperFaaS/tests/helpers"
	"log/slog"
	"sync"
	"time"
)

func TestOneNodeListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {

	testCase := config.Stats[0]

	statsChan := make(chan *[]*stats.StatusUpdate)
	errorChan := make(chan error, 10)
	wgWorkload := sync.WaitGroup{}

	wgWorkload.Add(1)
	go func(wg *sync.WaitGroup, errCh chan error) {

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, config.Workloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		errCh <- err
		wg.Done()
		statsChan <- expectedStats

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	resultChannel := make(chan []*stats.StatusUpdate)
	stopSignals := make(map[string]chan bool)

	nodeID := config.Stats[0].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, errCh chan error, wg *sync.WaitGroup) {
		stopSignal := stopSignals[nodeID]

		logger.Debug("Connecting listener node")
		//TODO: currently the nodes are running in the client node when using docker compose, which kinda misses the point
		actualNodeStats, err := helpers.ConnectNodeHelper(config.Config.ServerAddress, nodeID, logger, wg, stopSignal, time.Duration(testCase.Timeout)*time.Second)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}
		errCh <- err
		ch <- actualNodeStats
	}(resultChannel, errorChan, &wgNodes)

	wgWorkload.Wait()

	for len(errorChan) > 0 {
		e := <-errorChan
		if e != nil {
			return e
		}
	}
	expected := <-statsChan
	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := helpers.Evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
		return err
	}

	if result {
		logger.Info("=====TEST `oneNodeListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `oneNodeListening` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	if config.Config.Environment == "local" {
		testController.StatsManager.RemoveListener(nodeID)
	}

	return nil

}

func TestMultipleNodesListening(client pb.ControllerClient, testController controller.Controller, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {

	testCase := config.Stats[1]

	wgWorkload := sync.WaitGroup{}
	errorChan := make(chan error, 10)
	statsChan := make(chan *[]*stats.StatusUpdate)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup, errCh chan error) {

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, config.Workloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		errCh <- err
		logger.Debug("Workload is done")
		wg1.Done()
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	stopSignals := make(map[string]chan bool)
	resultChannels := make(map[string]chan []*stats.StatusUpdate)

	for _, nodeID := range config.Stats[1].NodeIDs {

		wgNodes.Add(1)
		resultChannels[nodeID] = make(chan []*stats.StatusUpdate, 1)
		ch := resultChannels[nodeID]

		go func(ch chan []*stats.StatusUpdate, nodeId string, errCh chan error) {
			stopSignal := stopSignals[nodeId]

			actualNodeStats, err := helpers.ConnectNodeHelper(config.Config.ServerAddress, nodeId, logger, &wgNodes, stopSignal, time.Duration(testCase.Timeout)*time.Second)
			if err != nil {
				logger.Error("Error when connecting node", "error", err)
			}
			errCh <- err
			ch <- actualNodeStats
			logger.Info("Sent stats to channel and now leaving goroutine", "nodeId", nodeID)
		}(ch, nodeID, errorChan)
	}

	wgWorkload.Wait()
	for len(errorChan) > 0 {
		var e error
		select {
		case e = <-errorChan:
		case <-time.After(10 * time.Second):
			e = fmt.Errorf("timeout occured when waitng for error")
		}
		if e != nil {
			return e
		}
	}
	expected := <-statsChan
	wgNodes.Wait()
	result := true

	for _, nodeID := range config.Stats[1].NodeIDs {
		receivedStats := <-resultChannels[nodeID]
		res, err := helpers.Evaluate(receivedStats, *expected)
		if err != nil {
			logger.Error("Error evaluating test results for node", "err", err, "node", nodeID)
			return err
		}

		if !res {
			result = false
		}

		if config.Config.Environment == "local" {
			testController.StatsManager.RemoveListener(nodeID)
		}
	}

	if result {
		logger.Info("=====TEST `multipleNodesListening` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `multipleNodesListening` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	return nil

}

func TestDisconnectAndReconnect(client pb.ControllerClient, testController controller.Controller, logger slog.Logger, spec helpers.ResourceSpec, config FullConfig) error {

	testCase := config.Stats[2]

	wgWorkload := sync.WaitGroup{}
	statsChan := make(chan *[]*stats.StatusUpdate)
	errorChan := make(chan error, 10)

	wgWorkload.Add(1)
	go func(wg1 *sync.WaitGroup, errCh chan error) {

		expectedStats, err := helpers.DoWorkloadHelper(client, logger, spec, config.Workloads[0])
		if err != nil {
			logger.Error("Error occurred in Test Case `testOneNodeListening`:", "error", err.Error())
		}

		logger.Debug("Workload is done")
		wg1.Done()
		errCh <- err
		statsChan <- expectedStats
		logger.Debug("Sent expected stats")

	}(&wgWorkload, errorChan)

	wgNodes := sync.WaitGroup{}

	stopSignals := make(map[string]chan bool)

	go func() {
		for _, nodeID := range config.Stats[2].NodeIDs {
			stopSignals[nodeID] = make(chan bool)
			logger.Info("Node will disconnect", "node", nodeID)
			time.Sleep(3 * time.Second)
			stopSignals[nodeID] <- true
		}
	}()

	resultChannel := make(chan []*stats.StatusUpdate, 10)

	nodeID := config.Stats[2].NodeIDs[0]

	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string, errCh chan error) {
		stopSignal := stopSignals[nodeID]

		actualNodeStats, err := helpers.ConnectNodeHelper(config.Config.ServerAddress, nodeID, logger, &wgNodes, stopSignal, time.Duration(testCase.Timeout)*time.Second)
		if err != nil {
			logger.Error("Error when connecting node", "error", err)
		}

		errCh <- err
		ch <- actualNodeStats
	}(resultChannel, nodeID, errorChan)

	wgWorkload.Wait()
	expected := <-statsChan
	for len(errorChan) > 0 {
		e := <-errorChan
		if e != nil {
			return e
		}
	}
	wgNodes.Add(1)

	go func(ch chan []*stats.StatusUpdate, nodeID string) {
		stopSignal := stopSignals[nodeID]
		actualNodeStats, err := helpers.ConnectNodeHelper(config.Config.ServerAddress, nodeID, logger, &wgNodes, stopSignal, time.Duration(testCase.Timeout)*time.Second)
		if err != nil {
			logger.Error("Error when reconnecting node", "error", err)
		}

		ch <- actualNodeStats
	}(resultChannel, nodeID)

	wgNodes.Wait()

	receivedStats := <-resultChannel
	result, err := helpers.Evaluate(receivedStats, *expected)
	if err != nil {
		logger.Error("Error evaluating test results", "err", err)
		return err
	}

	if result {
		logger.Info("=====TEST `DisconnectAndReconnect` COMPLETED: SUCCESS")
	} else {
		logger.Error("=====TEST `DisconnectAndReconnect` COMPLETED: FAILED")
		return fmt.Errorf("expected and actual stats are not equal")
	}

	if config.Config.Environment == "local" {
		testController.StatsManager.RemoveListener(nodeID)
	}
	return nil
}

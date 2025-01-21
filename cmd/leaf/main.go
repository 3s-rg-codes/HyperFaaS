package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/api"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/state"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

type workerIDs []string

func (i *workerIDs) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *workerIDs) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// start the leader

// handle configuration
// scrape intervals, batching of requests to the scheduler, etc.

func main() {
	workerIDs := workerIDs{}
	scrapeInterval := flag.Duration("scrape-interval", 50*time.Millisecond, "The interval at which to scrape the workers")
	flag.Var(&workerIDs, "worker-ids", "The IDs of the workers to manage")
	flag.Parse()

	// Logging with slog
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	scraper := state.NewScraper(*scrapeInterval, logger)

	// For now, we just set these once. In the future, we will have a way to update the worker IPs if needed.
	var ids []state.WorkerID
	for _, id := range workerIDs {
		ids = append(ids, state.WorkerID(id))
	}
	scraper.SetWorkerIDs(ids)

	workerState := make(state.WorkerStateMap)

	scheduler := scheduling.NewNaiveScheduler(workerState, ids, logger)

	server := api.NewLeafServer(scheduler)

	listener, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterLeafServer(grpcServer, server)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

}

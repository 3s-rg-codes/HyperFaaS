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
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scraping"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"google.golang.org/grpc"
)

type workerIPs []string

func (i *workerIPs) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *workerIPs) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// start the leader

// handle configuration
// scrape intervals, batching of requests to the scheduler, etc.

func main() {
	workerIPs := workerIPs{}
	scrapeInterval := flag.Duration("scrape-interval", 50*time.Millisecond, "The interval at which to scrape the workers")
	flag.Var(&workerIPs, "worker-ips", "The IP addresses of the workers to manage")
	flag.Parse()

	// Logging with slog
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	scraper := scraping.NewScraper(*scrapeInterval, logger)

	// For now, we just set these once. In the future, we will have a way to update the worker IPs if needed.
	scraper.SetWorkerIPs(workerIPs)

	scheduler := scheduling.NewNaiveScheduler(workerIPs, logger)

	server := api.NewLeafServer(scheduler, scraper)

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

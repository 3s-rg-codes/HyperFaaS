package api

import (
	"context"

	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scheduling"
	"github.com/3s-rg-codes/HyperFaaS/pkg/leaf/scraping"
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
)

// grpc api endpoints that leafLeader will expose

// these will be called by Leaders above in the leader chain

// The main "state" of the leader is all of the worker IPs.
// Would only be changed if a worker is added or removed.

type LeafServer struct {
	pb.UnimplementedLeafServer
	scheduler scheduling.Scheduler
	scraper   scraping.Scraper
}

func (s *LeafServer) FindInstance(ctx context.Context, req *pb.FindInstanceRequest) (*pb.FindInstanceResponse, error) {
	state, err := s.scraper.Scrape(ctx)
	if err != nil {
		return nil, err
	}

	decision, err := s.scheduler.Schedule(ctx, req.FunctionId, state)
	if err != nil {
		return nil, err
	}

	return &pb.FindInstanceResponse{InstanceId: decision[req.FunctionId], WorkerIp: decision[req.FunctionId]}, nil
}

func NewLeafServer(scheduler scheduling.Scheduler, scraper scraping.Scraper) *LeafServer {
	return &LeafServer{scheduler: scheduler, scraper: scraper}
}

package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/bfs/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"gonum.org/v1/gonum/graph/simple"
	"google.golang.org/grpc"
)

type bfsServer struct {
	pb.UnimplementedBFSServer
}

func (s *bfsServer) ComputeBFS(ctx context.Context, req *pb.BFSRequest) (*pb.BFSReply, error) {
	size := int(req.Size)
	if size <= 0 {
		size = 100 // default size
	}

	var seed *int
	if req.Seed != nil {
		seedVal := int(*req.Seed)
		seed = &seedVal
	}

	if seed != nil {
		rand.New(rand.NewSource(int64(*seed)))
	} else {
		rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	startGraph := time.Now()
	graph := generateBarabasiAlbert(size, 10)
	graphDuration := time.Since(startGraph).Microseconds()

	startBFS := time.Now()
	result := bfs(graph, 0)
	bfsDuration := time.Since(startBFS).Microseconds()

	return &pb.BFSReply{
		Result: result,
		Measurement: &pb.Measurement{
			GraphGeneratingTimeMicroseconds: graphDuration,
			ComputeTimeMicroseconds:         bfsDuration,
		},
	}, nil
}

// generateBarabasiAlbert creates a scale-free graph using a simple preferential attachment model
func generateBarabasiAlbert(n, m int) *simple.UndirectedGraph {
	g := simple.NewUndirectedGraph()

	if n <= 0 || m <= 0 {
		return g
	}

	// Initial fully-connected core of m nodes
	for i := 0; i < m; i++ {
		g.AddNode(simple.Node(i))
		for j := 0; j < i; j++ {
			g.SetEdge(g.NewEdge(simple.Node(i), simple.Node(j)))
		}
	}

	// Preferential attachment
	for i := m; i < n; i++ {
		newNode := simple.Node(i)
		g.AddNode(newNode)

		targets := preferentialTargets(g, m)
		for _, t := range targets {
			g.SetEdge(g.NewEdge(newNode, simple.Node(t)))
		}
	}

	return g
}

func preferentialTargets(g *simple.UndirectedGraph, m int) []int64 {
	var targets []int64
	seen := make(map[int64]bool)

	nodes := g.Nodes()
	var pool []int64

	for nodes.Next() {
		node := nodes.Node()
		degree := g.From(node.ID()).Len()
		for i := 0; i < degree; i++ {
			pool = append(pool, node.ID())
		}
	}

	for len(targets) < m && len(pool) > 0 {
		candidate := pool[rand.Intn(len(pool))]
		if !seen[candidate] {
			seen[candidate] = true
			targets = append(targets, candidate)
		}
	}

	return targets
}

func bfs(g *simple.UndirectedGraph, start int64) []int64 {
	visited := make(map[int64]bool)
	var result []int64
	var queue []int64

	queue = append(queue, start)
	visited[start] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		result = append(result, curr)

		neighbors := g.From(curr)
		for neighbors.Next() {
			n := neighbors.Node().ID()
			if !visited[n] {
				visited[n] = true
				queue = append(queue, n)
			}
		}
	}

	return result
}

func main() {
	fn := functionRuntimeInterface.NewV2(30)

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterBFSServer(reg, &bfsServer{})
	})
}

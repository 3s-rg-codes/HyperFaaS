package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"math/rand"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"gonum.org/v1/gonum/graph/simple"
)

type InputData struct {
	Size int
	Seed *int // Optional
}

type OutputData struct {
	Result      []int64
	Measurement struct {
		GraphGeneratingTimeMicroseconds int64
		ComputeTimeMicroseconds         int64
	}
}

func main() {
	f := functionRuntimeInterface.New(10)
	f.Ready(handler)
}

// inspired by https://github.com/spcl/serverless-benchmarks/blob/master/benchmarks/500.scientific/503.graph-bfs/python/function.py

func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {
	var input InputData
	if err := gob.NewDecoder(bytes.NewReader(in.Data)).Decode(&input); err != nil {
		return nil, fmt.Errorf("failed to decode input: %v", err)
	}

	if input.Seed != nil {
		rand.New(rand.NewSource(int64(*input.Seed)))
	} else {
		rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	startGraph := time.Now()
	graph := generateBarabasiAlbert(input.Size, 10)
	graphDuration := time.Since(startGraph).Microseconds()

	startBFS := time.Now()
	result := bfs(graph, 0)
	bfsDuration := time.Since(startBFS).Microseconds()

	output := OutputData{
		Result: result,
	}
	output.Measurement.GraphGeneratingTimeMicroseconds = graphDuration
	output.Measurement.ComputeTimeMicroseconds = bfsDuration

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(output); err != nil {
		return nil, fmt.Errorf("failed to encode response: %v", err)
	}

	return &common.CallResponse{
		Data:  buf.Bytes(),
		Error: nil,
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

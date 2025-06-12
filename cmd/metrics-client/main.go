package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"log"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	DB_PATH        = "./benchmarks/metrics.db"
	STATS_INTERVAL = 1 * time.Second
)

var (
	dbPath = flag.String("db-path", DB_PATH, "Path to SQLite database file")
)

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS status_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id TEXT NOT NULL,
			virtualization_type INTEGER NOT NULL,
			event INTEGER NOT NULL,
			status INTEGER NOT NULL,
			function_id TEXT NOT NULL,
			timestamp DATETIME NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS function_images (
			function_id TEXT PRIMARY KEY,
			image_tag TEXT NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS cpu_mem_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id TEXT NOT NULL,
			function_id TEXT,
			image_tag TEXT NOT NULL,
			timestamp INTEGER NOT NULL,

			-- CPU usage
			-- Units: nanoseconds (Linux)
			-- Units: 100's of nanoseconds (Windows)
			cpu_usage_total BIGINT,
			cpu_usage_percent FLOAT,

			-- Memory. Linux
			memory_usage BIGINT,
			memory_usage_limit BIGINT,
			memory_usage_percent FLOAT,

			-- Disk
			block_read FLOAT,
			block_write FLOAT,

			-- Network
			network_rx FLOAT,
			network_tx FLOAT
		)
	`)
	if err != nil {
		return err
	}

	// To avoid "database is locked" errors
	db.SetMaxOpenConns(1)

	return err
}

func main() {
	flag.Parse()

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	err = initDB(db)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := controller.NewControllerClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	stream, err := client.Status(ctx, &controller.StatusRequest{NodeID: "metrics-client"})
	if err != nil {
		log.Fatalf("Failed to get status stream: %v", err)
	}

	log.Println("Connected to status stream, waiting for updates...")

	go collectMetrics(db)

	for {
		update, err := stream.Recv()
		if err != nil {
			log.Printf("Error receiving update: %v", err)
			break
		}

		_, err = db.Exec(`
			INSERT INTO status_updates (
				instance_id, virtualization_type, event, status, 
				function_id, timestamp
			) VALUES (?, ?, ?, ?, ?, ?)`,
			update.InstanceId.Id,
			update.Type,
			update.Event,
			update.Status,
			update.FunctionId.Id,
			update.Timestamp.AsTime().Unix(),
		)
		if err != nil {
			log.Printf("Failed to insert update: %v", err)
			continue
		}
	}
}

// collectMetrics retrieves container stats periodically
func collectMetrics(db *sql.DB) {
	cli, err := createDockerClient()
	if err != nil {
		return
	}

	ticker := time.NewTicker(STATS_INTERVAL)
	defer ticker.Stop()

	for {
		<-ticker.C
		// Get all containers
		containers, err := cli.ContainerList(context.TODO(), container.ListOptions{
			All: true,
			Filters: filters.NewArgs(
				filters.Arg("name", "hyperfaas-"),
			),
		})
		if err != nil {
			log.Fatal(err)
		}

		// Get stats for each container
		for _, c := range containers {
			go func(cli *client.Client, db *sql.DB, c container.Summary) {
				stats, err := queryStats(context.TODO(), cli, c.ID)
				if err != nil {
					if err == io.EOF || client.IsErrNotFound(err) {
						// Container finished
						return
					}
					log.Printf("error: failed to get stats for container %v: %v", c.ID, err)
					return
				}
				if err := saveStats(db, stats, c.ID, c.Image); err != nil {
					log.Printf("error: couldn't save the stats: %v\n", err)
				}
			}(cli, db, c)
		}
	}
}

func createDockerClient() (*client.Client, error) {
	clientOpt := client.WithHost("unix:///var/run/docker.sock")
	cli, err := client.NewClientWithOpts(clientOpt, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// queryStats returns stats for a given container
func queryStats(ctx context.Context, cli *client.Client, containerID string) (*container.StatsResponse, error) {
	cs, err := cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, err
	}
	defer cs.Body.Close()

	var s container.StatsResponse
	if err := json.NewDecoder(cs.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveStats(db *sql.DB, s *container.StatsResponse, containerID string, image_tag string) error {
	mem := calculateMemUsageUnixNoCache(s.MemoryStats)
	memLimit := float64(s.MemoryStats.Limit)
	memPercent := calculateMemPercentUnixNoCache(memLimit, mem)

	previousCPU := s.PreCPUStats.CPUUsage.TotalUsage
	previousSystem := s.PreCPUStats.SystemUsage
	cpuPercent := calculateCPUPercent(previousCPU, previousSystem, s)

	instanceID := containerID[:12]

	netRx, netTx := calculateNetwork(s.Networks)

	// Assume Linux
	blockRead, blockWrite := calculateBlockIO(s.BlkioStats)

	_, err := db.Exec(`
		INSERT INTO cpu_mem_stats (
			instance_id, function_id, image_tag, timestamp,

			cpu_usage_total,
			cpu_usage_percent,

			memory_usage,
			memory_usage_limit,
			memory_usage_percent,

			block_read,
			block_write,

			network_rx,
			network_tx

		) VALUES (?, ?, ?, ?,
		 		  ?, ?, 
				  ?, ?, ?, 
				  ?, ?, 
				  ?, ?)`,
		instanceID,
		nil, // functionID is inserted later during import
		image_tag,
		s.Read.Unix(),
		s.CPUStats.CPUUsage.TotalUsage,
		cpuPercent,
		mem,
		memLimit,
		memPercent,
		blockRead,
		blockWrite,
		netRx,
		netTx,
	)

	return err
}

// calculateCPUPercentLinux calculates the average CPU usage percent over the last interval.
// inspired by https://github.com/docker/cli/blob/28.x/cli/command/container/stats_helpers.go
func calculateCPUPercent(previousCPU, previousSystem uint64, s *container.StatsResponse) float64 {
	var (
		cpuPercent = 0.0
		// calculate the change for the cpu usage of the container in between readings
		cpuDelta = float64(s.CPUStats.CPUUsage.TotalUsage) - float64(previousCPU)
		// calculate the change for the entire system between readings
		systemDelta = float64(s.CPUStats.SystemUsage) - float64(previousSystem)
		onlineCPUs  = float64(s.CPUStats.OnlineCPUs)
	)

	if onlineCPUs == 0.0 {
		onlineCPUs = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}
	return cpuPercent
}

// Taken from dcoker-cli
// calculateMemUsageUnixNoCache calculate memory usage of the container.
// Cache is intentionally excluded to avoid misinterpretation of the output.
//
// On cgroup v1 host, the result is `mem.Usage - mem.Stats["total_inactive_file"]` .
// On cgroup v2 host, the result is `mem.Usage - mem.Stats["inactive_file"] `.
//
// This definition is consistent with cadvisor and containerd/CRI.
// * https://github.com/google/cadvisor/commit/307d1b1cb320fef66fab02db749f07a459245451
// * https://github.com/containerd/cri/commit/6b8846cdf8b8c98c1d965313d66bc8489166059a
//
// On Docker 19.03 and older, the result was `mem.Usage - mem.Stats["cache"]`.
// See https://github.com/moby/moby/issues/40727 for the background.
func calculateMemUsageUnixNoCache(mem container.MemoryStats) float64 {
	// cgroup v1
	if v, isCgroup1 := mem.Stats["total_inactive_file"]; isCgroup1 && v < mem.Usage {
		return float64(mem.Usage - v)
	}
	// cgroup v2
	if v := mem.Stats["inactive_file"]; v < mem.Usage {
		return float64(mem.Usage - v)
	}
	return float64(mem.Usage)
}

func calculateMemPercentUnixNoCache(limit float64, usedNoCache float64) float64 {
	// MemoryStats.Limit will never be 0 unless the container is not running and we haven't
	// got any data from cgroup
	if limit != 0 {
		return usedNoCache / limit * 100.0
	}
	return 0
}

// claculateNetwork return bytes received and bytes sent
// Taken from docker-cli
func calculateNetwork(network map[string]container.NetworkStats) (float64, float64) {
	var rx, tx float64

	for _, v := range network {
		rx += float64(v.RxBytes)
		tx += float64(v.TxBytes)
	}
	return rx, tx
}

// calculateBlockIO returns total bytes read and written
// Taken from docker-cli
func calculateBlockIO(blkio container.BlkioStats) (uint64, uint64) {
	var blkRead, blkWrite uint64
	for _, bioEntry := range blkio.IoServiceBytesRecursive {
		if len(bioEntry.Op) == 0 {
			continue
		}
		switch bioEntry.Op[0] {
		case 'r', 'R':
			blkRead += bioEntry.Value
		case 'w', 'W':
			blkWrite += bioEntry.Value
		}
	}
	return blkRead, blkWrite
}

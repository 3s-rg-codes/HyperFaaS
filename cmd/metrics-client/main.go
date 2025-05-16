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
	DB_PATH = "./benchmarks/metrics.db"
	// DB_PATH        = "../../benchmarks/metrics.db"
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
			function_id TEXT NOT NULL,
			timestamp DATETIME NOT NULL,

			-- CPU usage
			-- Units: nanoseconds (Linux)
			-- Units: 100's of nanoseconds (Windows)
			cpu_total_usage BIGINT,

			-- Memory. Linux
			memory_usage BIGINT,
			memory_max_usage BIGINT
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
			update.Timestamp.AsTime(),
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
				if err := saveStats(db, stats, c.ImageID, c.ID); err != nil {
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
	cs, err := cli.ContainerStatsOneShot(ctx, containerID)
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

func saveStats(db *sql.DB, s *container.StatsResponse, functionID string, containerID string) error {
	_, err := db.Exec(`
		INSERT INTO cpu_mem_stats (
			instance_id, function_id, timestamp,

			cpu_total_usage,

			memory_usage, memory_max_usage
		) VALUES (?, ?, ?, ?, ?, ?)`,
		containerID,
		functionID,
		s.Read,
		s.CPUStats.CPUUsage.TotalUsage,
		s.MemoryStats.Usage,
		s.MemoryStats.MaxUsage,
	)

	return err
}

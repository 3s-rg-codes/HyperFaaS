package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"time"

	"github.com/3s-rg-codes/HyperFaaS/proto/controller"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	DB_PATH = "./benchmarks/metrics.db"
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

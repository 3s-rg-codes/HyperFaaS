package main

import (
	pb "github.com/3s-rg-codes/HyperFaaS/proto/leaf"
	"github.com/spf13/cobra"
	"log/slog"
	"os"
)

type CliStruct struct {
	leafConn pb.LeafClient
	logger   *slog.Logger
}

var Cli CliStruct

var rootCmd = &cobra.Command{
	Use:   "hyperfaas",
	Short: "the CliStruct for interacting with the HyperFaaS platform",
	Long:  "the CliStruct for interacting with the HyperFaaS platform - works by making gRPC calls to the platform",
	Run: func(cmd *cobra.Command, args []string) {
		println("testest")
	},
}

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "calling a HyperFaaS function; requires an imageTag amd data",
	Run: func(cmd *cobra.Command, args []string) {

	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(callCmd)
}

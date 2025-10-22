package main

import (
	"flag"

	"github.com/3s-rg-codes/HyperFaaS/pkg/utils"
)

func main() {
	var childAddrs utils.StringList

	address := flag.String("address", "0.0.0.0:50051", "Routing controller listen address")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat := flag.String("log-format", "text", "Log format (text, json, dev)")
	logFile := flag.String("log-file", "", "Optional log file path")

	flag.Var(&childAddrs, "child-addr", "Child address (repeat for multiple children)")

	flag.Parse()

	logger := utils.SetupLogger(*logLevel, *logFormat, *logFile)
	logger.Info("Starting Routing Controller",
		"address", *address,
		"children", childAddrs,
	)

}

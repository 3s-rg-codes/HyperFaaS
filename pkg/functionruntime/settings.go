package functionruntime

import (
	"os"
	"strconv"
)

const FunctionPort = "50052"

type settings struct {
	controllerAddress string
	instanceID        uint64
	functionID        uint64
	functionPort      string
}

func loadSettings() settings {
	controllerAddress, _ := os.LookupEnv("CONTROLLER_ADDRESS")
	instanceID, _ := parseUintEnv("INSTANCE_ID")
	functionID, _ := parseUintEnv("FUNCTION_ID")
	functionPort, ok := os.LookupEnv("FUNCTION_PORT")
	if !ok || functionPort == "" {
		functionPort = FunctionPort
	}
	return settings{
		controllerAddress: controllerAddress,
		instanceID:        instanceID,
		functionID:        functionID,
		functionPort:      functionPort,
	}
}

func parseUintEnv(key string) (uint64, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

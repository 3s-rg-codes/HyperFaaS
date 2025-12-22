package functionRuntimeInterface

import (
	"fmt"
	"os"
	"strconv"
)

type runtimeSettings struct {
	controllerAddress string
	functionID        string
	timeoutSeconds    int32
}

func loadRuntimeSettings() runtimeSettings {
	controllerAddress, ok := os.LookupEnv("CONTROLLER_ADDRESS")
	if !ok {
		fmt.Printf("Environment variable CONTROLLER_ADDRESS not found")
	}

	functionID, ok := os.LookupEnv("FUNCTION_ID")
	if !ok {
		fmt.Printf("Environment variable FUNCTION_ID not found")
	}

	timeoutSeconds, ok := os.LookupEnv("TIMEOUT_SECONDS")
	if !ok {
		fmt.Printf("Environment variable TIMEOUT_SECONDS not found")
	}
	timeoutSecondsInt, err := strconv.Atoi(timeoutSeconds)
	if err != nil {
		// default. TODO: find a better number for this .
		timeoutSecondsInt = 10
	}

	return runtimeSettings{
		controllerAddress: controllerAddress,
		functionID:        functionID,
		timeoutSeconds:    int32(timeoutSecondsInt),
	}
}

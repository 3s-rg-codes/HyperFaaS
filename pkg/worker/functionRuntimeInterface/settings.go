package functionRuntimeInterface

import (
	"fmt"
	"os"
)

type runtimeSettings struct {
	controllerAddress string
	functionID        string
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

	return runtimeSettings{
		controllerAddress: controllerAddress,
		functionID:        functionID,
	}
}

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var (
	// imageName
	imageName = flag.String("imageName", "hello", "Name of the image to build - 'all' for all images")
)

func main() {
	flag.Parse()

	localOs := runtime.GOOS
	var callerServerAddress string
	switch localOs {
	case "windows":
		callerServerAddress = "localhost"
	case "linux":
		callerServerAddress = "localhost"
	case "darwin":
		callerServerAddress = "host.docker.internal"
	default:
		callerServerAddress = "127.0.0.1"
	}

	functions := []string{"hello", "echo", "sleep", "crash"}
	dockerfile := fmt.Sprintf(`FROM alpine:latest

WORKDIR /root/

COPY handler /root/

RUN mkdir logs
RUN touch logs/fn.log
RUN apk add --no-cache --upgrade bash

COPY set_env.sh .
RUN chmod +x set_env.sh

# Expose port 50052 to the outside world
EXPOSE 50052
ENV CALLER_SERVER_ADDRESS=%s
# Command to run the executable
CMD ["sh", "-c" ,"source set_env.sh && echo $CONTAINER_ID && ./handler"]
`, callerServerAddress)

	if *imageName != "all" {
		buildExecutable(*imageName)
		buildDockerImage(*imageName, dockerfile)
		return
	} else {
		// Build Go executables for each function
		for _, fn := range functions {
			buildExecutable(fn)
		}

		// Build Docker images for each function
		for _, fn := range functions {
			buildDockerImage(fn, dockerfile)
		}

	}

}

func buildExecutable(fn string) {
	log.Printf("Building %s executable...\n", fn)
	cmd := exec.Command("go", "build", "-o", "handler", filepath.Join(fn, fn+".go"))
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to build %s: %s\n%s", fn, err, output)
	}
	log.Printf("Built %s executable successfully.\n", fn)

	// Move the built executable to the function's directory
	err = os.Rename("handler", filepath.Join(fn, "handler"))
	if err != nil {
		log.Fatalf("Failed to move handler: %s", err)
	}
}

func buildDockerImage(fn, dockerfile string) {
	log.Printf("Building Docker image for %s...\n", fn)

	// Write Dockerfile
	dockerfilePath := filepath.Join(fn, "Dockerfile")
	err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644)
	if err != nil {
		log.Fatalf("Failed to write Dockerfile: %s", err)
	}

	// Copy set_env.sh to the function's directory
	err = copyFile("set_env.sh", filepath.Join(fn, "set_env.sh"))
	if err != nil {
		log.Fatalf("Failed to copy set_env.sh: %s", err)
	}

	// Build Docker image
	cmd := exec.Command("docker", "build", "-t", fn, fn)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to build Docker image for %s: %s\n%s", fn, err, output)
	}
	log.Printf("Built Docker image for %s successfully.\n", fn)
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	return err
}

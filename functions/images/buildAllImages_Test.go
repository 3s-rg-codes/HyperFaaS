package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var allImages = []string{"hello", "echo", "sleep", "crash"}

type TestCase struct {
	testName      string
	ImageNames    []string
	all           bool
	ExpectedError bool
	Error         error
}

func TestOneImage(t *testing.T) {
	testCase := TestCase{
		testName:      "Testing build of hello",
		ImageNames:    []string{"hello"},
		all:           false,
		ExpectedError: false,
		Error:         nil,
	}

	t.Run(testCase.testName, func(t *testing.T) {
		origArgs := os.Args
		origCommandLine := flag.CommandLine

		defer func() {
			os.Args = origArgs
			flag.CommandLine = origCommandLine
		}()

		os.Args = cmdInput(testCase)
		flag.CommandLine = flag.NewFlagSet(testCase.testName, flag.ExitOnError)
		arg := flag.CommandLine.String("arg", "", "an argument")
		flag.Parse()

	})
}

func cmdInput(testCase TestCase) []string {
	var input []string
	input = append(input, "buildAllImages.go")
	var builder strings.Builder
	builder.WriteString("buildAllImages.go -imageName=")

	for i, iN := range testCase.ImageNames {
		builder.WriteString(iN)
		if i != len(testCase.ImageNames)-1 {
			builder.WriteString(",")
		}
	}
	input = append(input, builder.String())
	return input
}

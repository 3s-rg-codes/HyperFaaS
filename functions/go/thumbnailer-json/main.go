package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"github.com/3s-rg-codes/HyperFaaS/proto/common"
	"golang.org/x/image/draw"
)

type InputData struct {
	Image  []byte `json:"image"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func main() {
	f := functionRuntimeInterface.New(10)
	f.Ready(handler)
}

// Inspired by https://github.com/spcl/serverless-benchmarks/blob/master/benchmarks/200.multimedia/210.thumbnailer/python/function.py
func handler(ctx context.Context, in *common.CallRequest) (*common.CallResponse, error) {
	var input InputData
	if err := json.Unmarshal(in.Data, &input); err != nil {
		return nil, fmt.Errorf("failed to decode input: %v", err)
	}

	resized, err := resizeImage(input.Image, input.Width, input.Height)
	if err != nil {
		return nil, fmt.Errorf("resize failed: %v", err)
	}

	return &common.CallResponse{
		Data:  resized,
		Error: nil,
	}, nil
}

func resizeImage(input []byte, w, h int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, nil); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

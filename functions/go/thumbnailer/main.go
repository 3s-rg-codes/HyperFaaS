package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"image"
	"image/jpeg"
	"log"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"golang.org/x/image/draw"
)

type InputData struct {
	Image  []byte
	Width  int
	Height int
}

func main() {
	f := functionRuntimeInterface.New(10)
	f.Ready(handler)
}

//Inspired by https://github.com/spcl/serverless-benchmarks/blob/master/benchmarks/200.multimedia/210.thumbnailer/python/function.py

func handler(ctx context.Context, in *functionRuntimeInterface.Request) *functionRuntimeInterface.Response {
	var input InputData
	if err := gob.NewDecoder(bytes.NewReader(in.Data)).Decode(&input); err != nil {
		log.Printf("failed to decode input: %v", err)
		return &functionRuntimeInterface.Response{
			Data: []byte("invalid input"),
			Id:   in.Id,
		}
	}

	resized, err := resizeImage(input.Image, input.Width, input.Height)
	if err != nil {
		log.Printf("resize failed: %v", err)
		return &functionRuntimeInterface.Response{
			Data: []byte("resize failed"),
			Id:   in.Id,
		}
	}

	return &functionRuntimeInterface.Response{
		Data: resized,
		Id:   in.Id,
	}
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

package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"

	"github.com/3s-rg-codes/HyperFaaS/functions/go/thumbnailer/pb"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/functionRuntimeInterface"
	"golang.org/x/image/draw"
	"google.golang.org/grpc"
)

type thumbnailerServer struct {
	pb.UnimplementedThumbnailerServer
}

func (s *thumbnailerServer) Thumbnail(ctx context.Context, req *pb.ThumbnailRequest) (*pb.ThumbnailReply, error) {
	resized, err := resizeImage(req.Image, int(req.Width), int(req.Height))
	if err != nil {
		return nil, err
	}

	return &pb.ThumbnailReply{Image: resized}, nil
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

func main() {
	fn := functionRuntimeInterface.NewV2(30)

	fn.Ready(func(reg grpc.ServiceRegistrar) {
		pb.RegisterThumbnailerServer(reg, &thumbnailerServer{})
	})
}

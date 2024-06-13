package main

import (
	pb "github.com/3s-rg-codes/HyperFaaS/proto/function"
)

func handler(call *pb.Call) (*pb.Payload, error) {
	// TODO
	return &pb.Payload{Data: call.Data, Id: call.Id}, nil

}

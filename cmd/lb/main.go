package main

import "github.com/3s-rg-codes/HyperFaaS/proto/lb"

type lbServer struct {
	lb lb.LBClient
}

func main() {

	// flags

	// configure load balancer
	// it can talk to other LB nodes or leaf nodes

	// DI the load balancer strategy

	// start the load balancer

	// add out of band metric reporting
}

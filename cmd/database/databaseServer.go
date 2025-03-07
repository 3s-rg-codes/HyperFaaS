package main

import "github.com/3s-rg-codes/HyperFaaS/pkg/keyValueStore"

func main() {
	dbServer := keyValueStore.Store{
		Data: make(map[string]keyValueStore.PostRequest),
	}
	dbServer.Start()
}

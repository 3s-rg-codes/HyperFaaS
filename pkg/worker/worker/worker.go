package worker

import "github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"

type Config struct {
	Id         string
	Address    string
	Runtime    string
	Controller controller.Controller
}

type Worker struct {
	Id      string
	Address string
	Runtime string

	Controller *controller.Controller
}

func New(config *Config) (w Worker) {

	return Worker{
		Id:         config.Id,
		Address:    config.Address,
		Runtime:    config.Runtime,
		Controller: &config.Controller,
	}
}

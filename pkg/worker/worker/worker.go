package worker

import (
	"log/slog"

	"github.com/3s-rg-codes/HyperFaaS/pkg/worker/controller"
)

type Config struct {
	Id         string
	Address    string
	Runtime    string
	Controller controller.Controller
	logger     *slog.Logger
}

type Worker struct {
	Id      string
	Address string
	Runtime string

	Controller *controller.Controller
	logger     *slog.Logger
}

func New(config *Config, logger *slog.Logger) (w Worker) {

	return Worker{
		Id:         config.Id,
		Address:    config.Address,
		Runtime:    config.Runtime,
		Controller: &config.Controller,
		logger:     logger,
	}
}

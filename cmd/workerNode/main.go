package main

import (
	"flag"
	"os"

	cr "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime"
	dockerRuntime "github.com/3s-rg-codes/HyperFaaS/pkg/containerRuntime/docker"
	"github.com/3s-rg-codes/HyperFaaS/pkg/controller"
	"github.com/3s-rg-codes/HyperFaaS/pkg/worker"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type WorkerConfig struct {
	General struct {
		Id string `env:"WORKER_ID"`
	}
	Runtime struct {
		Type       string `env:"RUNTIME_TYPE"`
		AutoRemove bool   `env:"RUNTIME_AUTOREMOVE"`
	}

	Log struct {
		Level   string `env:"LOG_LEVEL"`
		Handler string `env:"LOG_HANDLER"`
	}
}

func parseArgs() (wc WorkerConfig) {

	flag.StringVar(&(wc.General.Id), "id", "", "Worker ID. (Env: WORKER_ID)")

	flag.StringVar(&(wc.Runtime.Type), "runtime", "", "Container runtime type. (Env: RUNTIME_TYPE)")
	flag.BoolVar(&(wc.Runtime.AutoRemove), "auto-remove", false, "Auto remove containers. (Env: RUNTIME_AUTOREMOVE)")
	flag.StringVar(&(wc.Log.Level), "log-level", "", "Log level (Env: LOG_LEVEL)")
	flag.StringVar(&(wc.Log.Handler), "log-handler", "", "Log handler (Env: LOG_HANDLER)")

	flag.Parse()
	return
}

func main() {
	var err error

	wc := parseArgs()

	//Setup Logging
	if wc.Log.Handler == "dev" {
		log.Logger = log.Output(
			zerolog.ConsoleWriter{
				Out:     os.Stderr,
				NoColor: false,
			},
		).With().Caller().Logger()

		zerolog.DisableSampling(true)
	} else if wc.Log.Handler != "prod" {
		log.Fatal().Msg("Log ExtHandler has to be either dev or prod")
	}

	log.Info().Msgf("Current configuration:\n%+v", wc)

	switch wc.Log.Level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Info().Msg("No Loglevel specified, using 'debug'")
	}

	if err != nil {
		log.Fatal().Err(err).Msg("TODO - handle error")
	}

	var runtime cr.ContainerRuntime
	// Runtime
	switch wc.Runtime.Type {
	case "docker":
		runtime = dockerRuntime.NewDockerRuntime(wc.Runtime.AutoRemove)
	default:
		log.Error().Msg("No runtime specified")
	}

	c := controller.New(runtime)

	w := worker.New(&worker.Config{
		Id:         wc.General.Id,
		Address:    "localhost:50051",
		Runtime:    wc.Runtime.Type,
		Controller: c,
	},
	)

	// Start the worker server
	w.Controller.StartServer()

}

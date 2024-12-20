package main

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
)

// start the leader

// handle configuration
// scrape intervals, batching of requests to the scheduler, etc.

func main() {
	// start the leader
	log.Logger = log.Output(
		zerolog.ConsoleWriter{
			Out:     os.Stderr,
			NoColor: false,
		},
	).With().Caller().Logger()

	zerolog.DisableSampling(true)

	log.Info().Msg("Starting leader")

	for {
		// handle configuration
		// scrape intervals, batching of requests to the scheduler, etc.
	}
}

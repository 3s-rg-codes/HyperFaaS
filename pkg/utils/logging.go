package utils

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/golang-cz/devslog"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc"
)

// InterceptorLogger returns a pre-configured grpc.UnaryServerInterceptor using slog for logging.
func InterceptorLogger(l *slog.Logger) grpc.UnaryServerInterceptor {
	opts := []logging.Option{
		logging.WithLogOnEvents(logging.FinishCall),
		logging.WithDisableLoggingFields(
			logging.ComponentFieldKey,
			logging.MethodTypeFieldKey,
			logging.SystemTag[0],
			logging.SystemTag[1],
			logging.ServiceFieldKey,
		),
	}
	adaptedLogger := logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		l.Log(ctx, slog.Level(lvl), msg, fields...)
	})
	return logging.UnaryServerInterceptor(adaptedLogger, opts...)
}

// SetupLogger sets up a slog logger with the given level, format, and file path.
func SetupLogger(level, format, filePath string) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{}

	switch level {
	case "debug":
		opts.Level = slog.LevelDebug
	case "info":
		opts.Level = slog.LevelInfo
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}

	writer := os.Stdout
	if filePath != "" {
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			panic(fmt.Sprintf("failed to open log file: %v", err))
		}
		writer = file
	}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "dev":
		handler = devslog.NewHandler(writer, &devslog.Options{
			HandlerOptions: opts,
		})
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	return slog.New(handler)
}

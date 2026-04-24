// Package logging configures structured JSON logging via log/slog for the
// pipeline's two binaries. Per Principle V of the project constitution,
// fmt.Print* and log.Print* are prohibited; all diagnostic output MUST flow
// through this logger.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// LogLevelToSlogLevel parses the canonical log-level strings accepted via
// LOG_LEVEL into slog.Level values.
func LogLevelToSlogLevel(logLevel string) (slog.Level, error) {
	switch strings.ToLower(logLevel) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level: %s", logLevel)
	}
}

// New constructs a JSON-handled *slog.Logger writing to the supplied sink
// (typically os.Stdout) at the specified level string. Returns an error if
// the level string is not recognised.
func New(w io.Writer, level string) (*slog.Logger, error) {
	lvl, err := LogLevelToSlogLevel(level)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})), nil
}

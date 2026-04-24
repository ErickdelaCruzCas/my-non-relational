package engine

import (
	"log/slog"
	"os"
	"strings"
)

var logger *slog.Logger

func init() {
	level := slog.LevelInfo
	if strings.ToLower(os.Getenv("LOG_LEVEL")) == "debug" {
		level = slog.LevelDebug
	}
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// LogInfo emits an INFO-level log from any package that imports engine.
func LogInfo(msg string, args ...any) {
	logger.Info(msg, args...)
}

// LogDebug emits a DEBUG-level log from any package that imports engine.
func LogDebug(msg string, args ...any) {
	logger.Debug(msg, args...)
}

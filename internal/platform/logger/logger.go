package logger

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

type Logger struct {
	service string
	base    *log.Logger
}

type Fields map[string]any

func New(service string) *Logger {
	return &Logger{
		service: service,
		base:    log.New(os.Stdout, "", 0),
	}
}

func (l *Logger) Info(message string, fields Fields) {
	l.write("info", message, fields)
}

func (l *Logger) Error(message string, fields Fields) {
	l.write("error", message, fields)
}

func (l *Logger) write(level string, message string, fields Fields) {
	if fields == nil {
		fields = Fields{}
	}

	entry := Fields{
		"time_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"level":    level,
		"service":  l.service,
		"message":  message,
	}

	for key, value := range fields {
		entry[key] = value
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		l.base.Printf(`{"level":"error","service":"%s","message":"failed to marshal log entry"}`, l.service)
		return
	}

	l.base.Println(string(raw))
}

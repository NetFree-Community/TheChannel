package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	logMu   sync.Mutex
	logFile = "/app/logs/auth.yaml"
)

func saveLoginFailedLog(title string, err error) {
	logMu.Lock()
	defer logMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		log.Println("Failed to create log directory:", err)
		return
	}

	f, fileErr := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if fileErr != nil {
		log.Println("Failed to open log file:", fileErr)
		return
	}
	defer f.Close()

	t := time.Now().Format(time.DateTime)
	errStr := err.Error()
	logEntry := map[string]any{
		"title":     title,
		"error":     errStr,
		"timestamp": t,
	}

	if _, err := f.WriteString("---\n"); err != nil {
		log.Println("Failed to write to log file:", err)
		return
	}
	if err := yaml.NewEncoder(f).Encode(logEntry); err != nil {
		log.Println("Failed to write to log file:", err)
		return
	}
}

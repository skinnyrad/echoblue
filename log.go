package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// logger serialises all output through one mutex (adapter goroutines
// would otherwise interleave lines) and prefixes a timestamp.
type logger struct {
	mu  sync.Mutex
	out io.Writer
}

var log = &logger{out: os.Stdout}

func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(l.out, stamp+" "+format+"\n", args...)
}

func logf(format string, args ...any) { log.Printf(format, args...) }

// setupLogFile additionally appends all output to path.
func setupLogFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	log.out = io.MultiWriter(os.Stdout, f)
	return nil
}

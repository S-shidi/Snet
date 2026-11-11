package client

import (
	"io"
	"strings"
	"sync"
)

type logRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

var ctlLogRing = &logRing{max: 2000}

// LogWriter returns a writer that keeps a bounded in-memory tail of daemon
// logs for /ctl/logs. It is intended to be combined with stderr/file output.
func LogWriter() io.Writer { return ctlLogRing }

func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		r.lines = append(r.lines, line)
	}
	if len(r.lines) > r.max {
		r.lines = append([]string(nil), r.lines[len(r.lines)-r.max:]...)
	}
	return len(p), nil
}

// LogTail returns the most recent n log lines as plain text.
func LogTail(n int) string {
	ctlLogRing.mu.Lock()
	defer ctlLogRing.mu.Unlock()
	if n <= 0 || n > len(ctlLogRing.lines) {
		n = len(ctlLogRing.lines)
	}
	if n == 0 {
		return ""
	}
	return strings.Join(ctlLogRing.lines[len(ctlLogRing.lines)-n:], "\n") + "\n"
}

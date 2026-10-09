package docker

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// MaxLogLine caps the length of a single log line sent to clients.
const MaxLogLine = 8 * 1024

// LogLine is one line of container output.
type LogLine struct {
	Stream string `json:"stream"` // stdout or stderr
	TS     string `json:"ts,omitempty"`
	Text   string `json:"text"`
}

// Logs streams container log lines to emit until the stream ends, ctx is done or emit fails.
func (r *Reader) Logs(ctx context.Context, id string, tail int, follow bool, emit func(LogLine) error) error {
	c, err := r.Container(ctx, id)
	if err != nil {
		return err
	}
	tty := c.Config != nil && c.Config.Tty

	rc, err := r.c.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     follow,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	var mu sync.Mutex
	locked := func(l LogLine) error {
		mu.Lock()
		defer mu.Unlock()
		return emit(l)
	}
	if tty {
		return scanLines(rc, "stdout", locked)
	}

	stdout := newLineWriter("stdout", locked)
	stderr := newLineWriter("stderr", locked)
	_, err = stdcopy.StdCopy(stdout, stderr, rc)
	if ferr := stdout.flush(); err == nil {
		err = ferr
	}
	if ferr := stderr.flush(); err == nil {
		err = ferr
	}
	return err
}

func scanLines(r io.Reader, stream string, emit func(LogLine) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if err := emit(parseLine(stream, sc.Text())); err != nil {
			return err
		}
	}
	return sc.Err()
}

// parseLine splits the RFC3339 timestamp Docker prepends and caps the text length.
func parseLine(stream, s string) LogLine {
	s = strings.TrimSuffix(s, "\r")
	ts, text, ok := strings.Cut(s, " ")
	if !ok || len(ts) < 20 || ts[4] != '-' {
		ts, text = "", s
	}
	if len(text) > MaxLogLine {
		text = text[:MaxLogLine] + "…"
	}
	return LogLine{Stream: stream, TS: ts, Text: text}
}

// lineWriter turns demultiplexed chunks back into lines.
type lineWriter struct {
	stream string
	emit   func(LogLine) error
	buf    bytes.Buffer
}

func newLineWriter(stream string, emit func(LogLine) error) *lineWriter {
	return &lineWriter{stream: stream, emit: emit}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		if err := w.emit(parseLine(w.stream, strings.TrimSuffix(line, "\n"))); err != nil {
			return 0, err
		}
	}
	if w.buf.Len() > MaxLogLine*2 {
		return len(p), w.flush()
	}
	return len(p), nil
}

func (w *lineWriter) flush() error {
	if w.buf.Len() == 0 {
		return nil
	}
	line := w.buf.String()
	w.buf.Reset()
	return w.emit(parseLine(w.stream, line))
}

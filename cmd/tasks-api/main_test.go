package main

import (
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestVersionDoesNotRequireTaskConfiguration(t *testing.T) {
	if status := run([]string{"--version"}); status != 0 {
		t.Fatalf("--version status = %d", status)
	}
}

func TestUnconfiguredServerRefusesBeforeListening(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root+"/config")
	for _, key := range []string{"TASKS_DIR", "TASKS_FILE", "TASKS_ARCHIVE", "TASKS_MEMORY"} {
		t.Setenv(key, "")
	}
	if status := run(nil); status != 1 {
		t.Fatalf("unconfigured status = %d", status)
	}
}

// streamingHandler stands in for an open /events stream: a request that never
// returns until CloseStreams tells it to.
type streamingHandler struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (h *streamingHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(200)
	writer.(http.Flusher).Flush()
	close(h.started)
	<-h.closed
}

func (h *streamingHandler) CloseStreams() { h.once.Do(func() { close(h.closed) }) }

// A stream must not hold shutdown for the whole drain timeout: serve registers
// CloseStreams, so a stop ends open streams and returns promptly.
func TestShutdownEndsOpenStreamsPromptly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := &streamingHandler{started: make(chan struct{}), closed: make(chan struct{})}
	stop := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() { result <- serveUntil(listener, handler, stop) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-handler.started

	stop <- os.Interrupt
	select {
	case status := <-result:
		if status != 0 {
			t.Fatalf("status = %d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited on the open stream")
	}
}

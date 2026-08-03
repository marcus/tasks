// Command tasks-api is the Go port of bin/tasks-api: the loopback HTTP surface
// over one task store.
//
// The Ruby entry point resolves configuration, prints two lines, and execs
// puma against config.ru. This one resolves the same configuration, prints the
// same two lines, and serves net/http directly — there is no application
// server to hand off to, and adding one would be a dependency the port does not
// need.
//
// Loopback only, deliberately and in three places: the flag refuses any other
// bind address, the listener binds 127.0.0.1, and the server refuses a Host
// header that is not this port on 127.0.0.1 or localhost.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"tasks-go/internal/api"
	"tasks-go/internal/application"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
	"tasks-go/internal/temporal"
	"tasks-go/internal/updatestamp"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	flags := flag.NewFlagSet("tasks-api", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bind := flags.String("bind", "127.0.0.1", "Bind address (127.0.0.1 only)")
	port := flags.Int("port", 4747, "Loopback port (default 4747)")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tasks-api [--port PORT]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tasks-api does not accept positional arguments")
		return 1
	}
	if *bind != "127.0.0.1" {
		fmt.Fprintln(os.Stderr, "tasks-api supports only the 127.0.0.1 loopback bind")
		return 1
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "tasks-api port must be between 1 and 65535")
		return 1
	}

	env := determinism.OSEnv()
	paths := config.Resolve(repoRoot(), env, nil)
	for _, warning := range paths.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}

	server, err := buildServer(paths, env, *port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-api: "+err.Error())
		return 1
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tasks-api: "+err.Error())
		return 1
	}
	fmt.Printf("tasks-api listening on http://127.0.0.1:%d\n", *port)
	fmt.Printf("tasks source: %s; archive source: %s\n",
		paths.Sources["org"], paths.Sources["archive"])

	return serve(listener, server)
}

// serve runs until SIGINT or SIGTERM, then drains. Both signals exit 0: the
// black-box test that stops the Ruby server with INT asserts a clean status,
// and a server that reported failure for an ordinary shutdown would be wrong
// in exactly the way a supervisor notices.
func serve(listener net.Listener, handler http.Handler) int {
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "tasks-api: "+err.Error())
			return 1
		}
		return 0
	case <-stop:
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
		return 0
	}
}

// buildServer wires the shared application facade, the checked reader, and the
// changeset seam onto one store pair.
//
// Every one of the three builds a FRESH store per call. That is the whole of
// this surface's concurrency design: two simultaneous requests never share a
// store value, and the coordination that does matter — the file lock, the
// post-write validation, the revision precondition — lives inside the store
// where one process's threads and a second process's CLI are equally covered.
func buildServer(paths config.Paths, env determinism.Env, port int) (*api.Server, error) {
	writeOptions := store.Options{
		JournalDir: journal.DirFor(paths.Org, env),
		Device:     updatestamp.Device(env),
		MaxDepth:   paths.MaxDepth,
	}
	if clock := determinism.Clock(env); clock != nil {
		writeOptions.Now = clock
	}
	if sequence, err := determinism.SharedIDSource(env); err == nil && sequence != nil {
		writeOptions.IDSource = sequence.Call
	}
	newStore := func() *store.Store {
		options := writeOptions
		// One coalescing scope per REQUEST, not per process: the API's writes
		// are unrelated to each other even when they arrive milliseconds apart,
		// and a shared scope would let one client's edit extend another's undo
		// step.
		options.CoalesceScope = ""
		return store.NewWriter(paths.Org, paths.Archive, options)
	}

	temporalContext := func() temporal.Context {
		built, err := temporal.NewContext(time.Now().UTC(), paths.Timezone, paths.TimeFormat)
		if err != nil {
			return temporal.Context{Now: time.Now().UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
		}
		return built
	}
	queryOptions := []taskquery.Option{taskquery.WithLinkConfig(paths.Links, paths.LinkSystems)}

	app, err := application.New(application.Options{
		Factory:         func() application.Store { return newStore() },
		TemporalContext: temporalContext,
		HostContext:     paths.HostContext,
		QueryOptions:    queryOptions,
	})
	if err != nil {
		return nil, err
	}

	return api.New(api.Options{
		App:             app,
		Read:            api.NewStoreReader(newStore, temporalContext, queryOptions...),
		Changesets:      func() api.Changesets { return newStore() },
		TemporalContext: temporalContext,
		QueryOptions:    queryOptions,
		Port:            port,
		MaxDepth:        paths.MaxDepth,
		UrgentDays:      paths.UrgentDays,
		Timezone:        paths.Timezone,
		TimeFormat:      paths.TimeFormat,
		Logger:          os.Stderr,
	})
}

// repoRoot is bin/tasks-api's ROOT: the repository the binary was built into,
// used only as the last fallback for where the task files live.
func repoRoot() string {
	executable, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(executable)))
}

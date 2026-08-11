package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/exequieldeferrari/axiom/internal/claude"
	"github.com/exequieldeferrari/axiom/internal/event"
	"github.com/exequieldeferrari/axiom/internal/otlp"
	"github.com/exequieldeferrari/axiom/internal/store"
)

// DefaultAddr is where the receiver listens. It is the standard OTLP/HTTP port
// on the loopback interface only: telemetry describes what a developer is
// working on, and it has no business leaving the machine.
const DefaultAddr = "127.0.0.1:4318"

// shutdownGrace bounds how long a stop waits for in-flight exports.
const shutdownGrace = 2 * time.Second

// runObserve records agent telemetry until interrupted.
func runObserve(args []string, stdout io.Writer) error {
	addr, err := parseObserveFlags(args)
	if err != nil {
		return err
	}

	dir, err := store.DefaultDir()
	if err != nil {
		return err
	}
	s, err := store.OpenUsage(dir)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%w\nanother process may already be listening; try 'axiom observe --addr 127.0.0.1:4319'", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return observe(ctx, listener, s, stdout, time.Now)
}

func parseObserveFlags(args []string) (string, error) {
	flags := flag.NewFlagSet("observe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	addr := flags.String("addr", DefaultAddr, "address to listen on")
	if err := flags.Parse(args); err != nil {
		return "", &UsageError{Msg: err.Error()}
	}
	if flags.NArg() > 0 {
		return "", &UsageError{Msg: fmt.Sprintf("unexpected argument %q", flags.Arg(0))}
	}
	return *addr, nil
}

// recorder writes usage records and counts what it saw.
//
// The receiver calls it from request goroutines, so the mutex guards both the
// append and the counters.
type recorder struct {
	store  *store.Store[event.Usage]
	out    io.Writer
	mu     sync.Mutex
	kept   int
	failed int
}

func (r *recorder) record(records []otlp.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rec := range records {
		// Most of what an agent exports is not a measurement: prompts, plugin
		// inventories and hook bookkeeping are all dropped here, before
		// anything is written.
		u, ok := claude.Usage(rec)
		if !ok {
			continue
		}
		if err := r.store.Append(u); err != nil {
			r.failed++
			continue
		}
		r.kept++
		fmt.Fprintln(r.out, describeUsage(u))
	}
}

// describeUsage renders one record from its own fields only. Nothing here is
// looked up in the behavioral event log: the two streams stay independent
// until a later milestone joins them deliberately.
func describeUsage(u event.Usage) string {
	when := u.Timestamp.Local().Format(time.TimeOnly)
	switch u.Kind {
	case event.UsageToolResult:
		return fmt.Sprintf("  %s  tool_result    %-16s %s returned", when, u.ToolName, size(u.ResultBytes))
	case event.UsageModelRequest:
		return fmt.Sprintf("  %s  model_request  %-16s %s", when, u.Model, describeTokens(u.Tokens))
	default:
		return fmt.Sprintf("  %s  %s", when, u.Kind)
	}
}

func describeTokens(t *event.Tokens) string {
	if t == nil {
		return "tokens not reported"
	}
	return fmt.Sprintf("%d in · %d out · %d cache read · %d cache write",
		t.Input, t.Output, t.CacheRead, t.CacheCreation)
}

func size(n *int64) string {
	if n == nil {
		return "an unreported number of bytes"
	}
	if *n < 1024 {
		return fmt.Sprintf("%d B", *n)
	}
	return fmt.Sprintf("%.1f KB", float64(*n)/1024)
}

// observe serves until ctx is cancelled, then reports what it recorded.
func observe(ctx context.Context, listener net.Listener, s *store.Store[event.Usage], stdout io.Writer, now func() time.Time) error {
	rec := &recorder{store: s, out: stdout}
	receiver := otlp.NewReceiver(rec.record)

	fmt.Fprintf(stdout, "Axiom is listening on %s\n", claude.TelemetryEndpoint(listener.Addr().String()))
	fmt.Fprintf(stdout, "Recording to %s\n\n", s.Path())
	fmt.Fprint(stdout, "Run 'axiom init --telemetry' if Claude Code is not configured to export yet.\n")
	fmt.Fprint(stdout, "Press Ctrl-C to stop.\n\n")

	srv := &http.Server{Handler: receiver, ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	started := now()
	select {
	case err := <-errs:
		if err != nil {
			return fmt.Errorf("receive telemetry: %w", err)
		}
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = srv.Shutdown(shutdown)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	fmt.Fprintf(stdout, "\nRecorded %s in %s.\n",
		plural(rec.kept, "usage record"), now().Sub(started).Round(time.Second))
	if rec.failed > 0 {
		fmt.Fprintf(stdout, "%s could not be written.\n", plural(rec.failed, "record"))
	}
	if n := receiver.Rejected(); n > 0 {
		fmt.Fprintf(stdout, "%s rejected as unreadable.\n", plural(int(n), "export"))
	}
	return nil
}

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/core/module/handler"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
)

// syncBuffer collects a child process's output; exec writes it from another
// goroutine while the test polls it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func writeTestConfig(t *testing.T, port string) string {
	t.Helper()
	cfg := fmt.Sprintf(`appName: "shutdownTest"
log:
  level: "debug"
  destinations:
    - type: "stdout"
pluginManager:
  root: "/tmp"
http:
  port: "%s"
`, port)
	path := filepath.Join(t.TempDir(), "adapter.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func stubManagerAndServer(t *testing.T, srv http.Handler, closer func()) {
	t.Helper()
	origMgr, origSrv := newManagerFunc, newServerFunc
	newManagerFunc = func(context.Context, *plugin.ManagerConfig) (*plugin.Manager, func(), error) {
		return &plugin.Manager{}, closer, nil
	}
	newServerFunc = func(context.Context, handler.PluginManager, *Config) (http.Handler, error) {
		return srv, nil
	}
	t.Cleanup(func() { newManagerFunc, newServerFunc = origMgr, origSrv })
}

// A server that cannot bind its port must make run return the error. Before
// the fix run logged it and then waited for a context that nothing cancels:
// the process stayed up, serving nothing, with plugins and background loops
// still running.
func TestRun_ListenFailureReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	closed := make(chan struct{})
	stubManagerAndServer(t, http.NewServeMux(), func() { close(closed) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err = run(ctx, writeTestConfig(t, port))
	if err == nil {
		t.Fatalf("run returned nil after %s although port %s was already in use", time.Since(start).Round(time.Millisecond), port)
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("unexpected error: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("run took %s to report the listen failure", d)
	}
	select {
	case <-closed:
	default:
		t.Error("closers were not run after the listen failure")
	}
}

// In-flight requests must be allowed to finish when the context is cancelled,
// and the closers must run after they have.
func TestRun_CancelDrainsInFlightRequestThenRunsClosers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()

	inHandler := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(inHandler)
		<-release
		_, _ = w.Write([]byte("done"))
	})

	closerRan := make(chan struct{})
	handlerDoneBeforeCloser := false
	handlerDone := make(chan struct{})
	stubManagerAndServer(t, mux, func() {
		select {
		case <-handlerDone:
			handlerDoneBeforeCloser = true
		default:
		}
		close(closerRan)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, writeTestConfig(t, port)) }()

	// Wait for the server, then start a request that blocks in the handler.
	url := "http://127.0.0.1:" + port + "/slow"
	respCh := make(chan string, 1)
	go func() {
		var lastErr error
		for i := 0; i < 100; i++ {
			resp, err := http.Get(url)
			if err != nil {
				lastErr = err
				time.Sleep(20 * time.Millisecond)
				continue
			}
			buf := make([]byte, 16)
			n, _ := resp.Body.Read(buf)
			resp.Body.Close()
			close(handlerDone)
			respCh <- string(buf[:n])
			return
		}
		respCh <- "error: " + lastErr.Error()
	}()

	select {
	case <-inHandler:
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached the handler")
	}

	cancel() // what SIGTERM does
	time.Sleep(100 * time.Millisecond)
	select {
	case <-closerRan:
		t.Fatal("closers ran while a request was still in flight")
	default:
	}
	close(release)

	if got := <-respCh; got != "done" {
		t.Errorf("in-flight request got %q, want the full response", got)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	<-closerRan
	if !handlerDoneBeforeCloser {
		t.Error("closers ran before the in-flight request completed")
	}
}

// TestHelperMainWaitsForSignal is not a test: it is the child process of
// TestMain_SIGTERMCancelsRunContext. It runs main() with run replaced by a
// function that reports whether its context was cancelled.
func TestHelperMainWaitsForSignal(t *testing.T) {
	if os.Getenv("ONIX_SHUTDOWN_HELPER") != "1" {
		t.Skip("helper process only")
	}
	runFunc = func(ctx context.Context, _ string) error {
		fmt.Println("HELPER_READY")
		select {
		case <-ctx.Done():
			fmt.Println("HELPER_CONTEXT_CANCELLED")
		case <-time.After(10 * time.Second):
			fmt.Println("HELPER_TIMED_OUT")
		}
		return nil
	}
	os.Args = []string{os.Args[0]}
	main()
}

// SIGTERM is how Kubernetes and Docker stop the adapter. It must cancel the
// context run works under, so the server drains and the closers run. Without
// a handler the Go runtime kills the process on the spot.
func TestMain_SIGTERMCancelsRunContext(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperMainWaitsForSignal$", "-test.v")
	cmd.Env = append(os.Environ(), "ONIX_SHUTDOWN_HELPER=1")
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "HELPER_READY") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("helper never became ready; output:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err != nil {
		t.Fatalf("process did not exit cleanly on SIGTERM: %v; output:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "HELPER_CONTEXT_CANCELLED") {
		t.Errorf("SIGTERM did not cancel the run context; output:\n%s", out.String())
	}
}

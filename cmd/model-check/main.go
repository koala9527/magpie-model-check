package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"github.com/koala9527/magpie-model-check/internal/modelcheck"
)

func main() {
	dir := flag.String("data", "data", "private local data directory")
	listen := flag.String("listen", "127.0.0.1:3479", "loopback page service address")
	owned := flag.Bool("owned", false, "read private boot configuration from stdin and stop at EOF")
	flag.Parse()
	if err := run(*dir, *listen, *owned); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(dir, addr string, owned bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("INVALID_LISTEN_ADDRESS")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("LOOPBACK_LISTENER_REQUIRED")
	}
	if os.MkdirAll(dir, 0700) != nil {
		return errors.New("DATA_DIRECTORY_UNAVAILABLE")
	}
	lock := flock.New(filepath.Join(dir, "runtime.lock"))
	deadline := time.Now().Add(4 * time.Second)
	for {
		acquired, err := lock.TryLock()
		if err != nil {
			return errors.New("DATA_LOCK_FAILED")
		}
		if acquired {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("DATA_DIRECTORY_IN_USE")
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer lock.Unlock()
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 1024*1024))
	cfg := modelcheck.Config{}
	if owned {
		line, err := reader.ReadString('\n')
		if err != nil || len(line) > 65536 || json.Unmarshal([]byte(line), &cfg) != nil {
			return errors.New("PRIVATE_BOOT_CONFIG_INVALID")
		}
	}
	s, err := modelcheck.New(dir, cfg)
	if err != nil {
		return errors.New("MODEL_CHECK_START_FAILED")
	}
	defer s.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.New("PAGE_SERVICE_PORT_IN_USE")
	}
	origin := "http://" + ln.Addr().String()
	control, page := modelcheck.Secret(), modelcheck.Secret()
	server := &http.Server{Handler: s.Handler(control, page, origin), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"origin": origin, "control": control}); err != nil {
		ln.Close()
		return errors.New("PARENT_CHANNEL_CLOSED")
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	parentClosed := make(chan struct{})
	if owned {
		go func() { io.Copy(io.Discard, reader); close(parentClosed) }()
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	select {
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("PAGE_SERVICE_FAILED")
		}
	case <-stop:
	case <-parentClosed:
	}
	// Cancel network work before waiting for HTTP readers or closing SQLite.
	s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	return nil
}

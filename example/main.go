package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fornaxian.tech/zerodown"
)

// This example starts a HTTP server on port 8080. The server returns a slow
// response (lasting one minute) at /slow, and you can restart the server by
// requesting /restart
//
// The server also works with Systemd socket activation. To test this you can
// build the executable with `go build main.go` and then run it with:
//
//	systemd-socket-activate --listen=8080 ./main
//
// The server will be started on the first request. The listener is passed down
// to the child process with ExtraFiles
func main() {
	// If this is the parent process we create the listener and pass it through
	// to the child. If we have a systemd socket, as indicated by LISTEN_FDS, we
	// don't need to open the listener ourselves. Systemd will automatically
	// pass the socket file down to all child processes
	if os.Getenv("LISTEN_FDS") != "" {
		fmt.Println("We received a socket from systemd")

		// Sighup doesn't work with systemd-socket-activate for some reason.
		// systemd itself catches the hangup signal and doesn't pass it down.
		// Using a different signal works
		zerodown.ReloadSignals = []os.Signal{syscall.SIGUSR2}

	} else if zerodown.IsParent() {
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: 8080})
		panicOnErr(err)

		file, err := listener.File()
		panicOnErr(err)

		zerodown.ExtraFiles = []*os.File{file}
	}

	if zerodown.Init() {
		return
	}

	// Get the socket from the parent process and start a server with it
	listener, err := net.FileListener(os.NewFile(3, "MyListener"))
	panicOnErr(err)

	var server = exampleServer(listener)

	zerodown.StartupFinished()

	stopOnSignal(server)
}

func exampleServer(l net.Listener) (server *http.Server) {
	var mux = http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello")
	})

	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		var tick = time.NewTicker(time.Second)
		defer tick.Stop()

		var i = 0
		for t := range tick.C {
			fmt.Fprintf(
				w,
				"Hello, %d seconds have passed since this request started, "+
					"the current time is %s. Have a nice day.\n",
				i, t,
			)

			// Flush the write buffer so the message appears instantly
			w.(http.Flusher).Flush()

			i++
			if i == 60 {
				break
			}
		}
	})

	mux.HandleFunc("/restart", func(w http.ResponseWriter, r *http.Request) {
		if err := zerodown.Restart(); err != nil {
			fmt.Fprintf(w, "Could not restart server: %s\n", err)
		} else {
			fmt.Fprintln(w, "Restarted")
		}
	})
	server = &http.Server{Handler: mux}
	go server.Serve(l)

	fmt.Println("Started HTTP server")
	return server
}

func stopOnSignal(server *http.Server) {
	var signals = make(chan os.Signal, 1)
	signal.Notify(signals, zerodown.StopSignals...)

	fmt.Printf("Caught signal %s, stopping HTTP server\n", <-signals)

	var ctx, cancel = context.WithTimeout(context.Background(), time.Hour*48)
	if err := server.Shutdown(ctx); err != nil {
		panic(fmt.Errorf("graceful shutdown failed: %w", err))
	}
	cancel()

	fmt.Println("HTTP server stopped")
}

func panicOnErr(err error) {
	if err != nil {
		panic(err)
	}
}

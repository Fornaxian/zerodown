# Fornaxian Zerodown

Extremely simple zero-downtime deployments. Used in
[pixeldrain.com](https://pixeldrain.com)

Use in combination with https://github.com/libp2p/go-reuseport.

## How does it work?

Zerodown captures the initialization of your application to create a parent and
a child process. The parent process starts your application as a child. All
arguments and variables are passed through.

When the SIGHUP signal is caught by the parent process it starts a new child
process from the same executable. If the executable has been updated it will run
the updated code.

## Usage

`zerodown.Init()` should be the very first thing your program runs when
starting. Right at the top of the main function. Then you can start your whole
application server. Listen on ports, connect to the database, etc.

When you are ready to serve requests you call `zerodown.StartupFinished()`. This
will tell the parent process that you are done and it will shutdown the previous
process.

## Example

```go
func main() {
	// Initialize the zerodown parent process. When this function returns true
	// the process must exit
	if zerodown.Init() {
		return
	}

	// Initialize your server: Connect to database, listen on a port, start a
	// server, etc
	listener, err := reuseport.Listen("tcp", ":443");
	if err != nil {
		panic(err)
	}

	// Tell zerodown that this process is ready to serve requests. This will
	// cause the previous process to shut down.
	zerodown.StartupFinished()

	// Listen for signals and exit when done. Zerodown will send a SIGINT signal
	// when it wants us to shut down. It's very important that we listen for
	// that one
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	sig := <-signals
	fmt.Printf("Caught %s signal, stopping...\n", sig)

	listener.Close()
}
```

## Creating Listeners

If you're running a server application of some kind you'll need a socket to
listen on. Normally only a single process can listen on a TCP port at one time,
luckily for us SO_REUSEPORT exists. This allows multiple processes to listen on
a network port. Now we can seamlessly pass traffic from the old process to the
new process without any downtime.

Another approach is to start the listeners in the parent process and pass them
to the child as file descriptors. This can be done by using the net.ListenTCP()
function to get your listener and calling File() on it:

```go
listener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: 443})
file, err := listener.File()
zerodown.ExtraFiles = []*os.File{file}
```

The child can then get the file descriptor from the parent and convert it back
to a listener:

```go
listener := net.FileListener(os.NewFile(3, "MyListener"))
```

## Example systemd service file

With this systemd service you can use `systemctl reload` to reload your server.

```
[Unit]
Description=My API server
After=network.target

[Service]
Type=simple
ExecStart=/bin/myapiserver

KillMode=control-group
KillSignal=SIGINT

# The HUP signal tells zerodown to restart the child process
ExecReload=kill -HUP $MAINPID

# Allow non-root process to bind to privileged ports
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

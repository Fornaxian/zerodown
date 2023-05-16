package zerodown

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var (
	// Process ID of the parent process
	parentPID = 0

	Logger = log.Default()

	// Signals which cause the child process to be restarted
	ReloadSignals = []os.Signal{syscall.SIGHUP}

	// Signals which cause the server to shut down
	StopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

	// Signals to pass through to the child process
	PassthroughSignals []os.Signal

	// Signal the child process sends to the parent process to indicate that
	// initialization is finished. Make this this is the same on both the parent
	// and the child
	StartupFinishedSignal = syscall.SIGUSR1

	// Extra file descriptors to pass to the child process
	ExtraFiles []*os.File

	// Extra environment variables to pass to the child process
	ExtraVariables []string
)

// Initialize the zerodown parent process. This should be the very first thing
// your main function calls. If this function returns exit=true then your main
// function should return.
//
// Correct usage:
//
//	func main() {
//		if zerodown.Init() {
//			return
//		}
//
//		// Initialize your server: Connect to database, listen on a port, etc
//
//		zerodown.StartupFinished()
//
//		// Listen for signals and exit when done
//	}
func Init() (exit bool) {
	// Check the parent PID. If the PID is undefined it means that we are the
	// parent. If this is a child process we return and tell the main function
	// to carry on with initialization
	if IsChild() {
		print("Our PID: %d. Parent PID: %d", os.Getpid(), parentPID)
		return false
	}

	print("Parent process started with PID %d. Starting child and listening for signals...", os.Getpid())

	// Start the child process
	if err := restart(); err != nil {
		panic(fmt.Errorf("failed to start child process: %w", err))
	}

	var signals = make(chan os.Signal, 1)
	signal.Notify(signals, combineSlices(ReloadSignals, StopSignals, PassthroughSignals)...)

	for sig := range signals {
		if inArray(sig, ReloadSignals) {
			print("Reload signal caught! Restarting child process")

			if err := restart(); err != nil {
				panic(fmt.Errorf("failed to start child process: %w", err))
			}
		} else if inArray(sig, StopSignals) {
			print("Interrupt caught. Stopping child processes...")

			stopChild(childProcess)
			shutdownWG.Wait()

			print("All child processes ended. Shutdown complete!")
			return true
		} else if inArray(sig, PassthroughSignals) {
			print("Caught signal %s, passing through to child", sig)

			if childProcess != nil {
				if err := childProcess.Signal(syscall.SIGINT); err != nil {
					print("Failed to send signal to child process %d: %s", childProcess.Pid, err)
				}
			}
		}
	}

	return true
}

func StartupFinished() {
	if parentPID != 0 {
		print("Signaling to parent process %d that initialization is finished", parentPID)

		process, err := os.FindProcess(parentPID)
		if err != nil {
			panic(fmt.Errorf("could not find parent process: %w", err))

		}
		if err = process.Signal(StartupFinishedSignal); err != nil {
			panic(fmt.Errorf("could not signal parent process: %w", err))
		}
	} else {
		panic("StartupFinished should not be called on the parent process itself")
	}
}

var shutdownWG = &sync.WaitGroup{}
var totalChildren = 0
var childProcess *os.Process

func restart() (err error) {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable name: %w", err)
	}

	// We strip the first argument since that contains the executable name
	var cmd = exec.Command(executable, os.Args[1:]...)

	// Pass the parent environment to the child. This contains important things
	// like PATH and other variables set by the OS
	cmd.Env = combineSlices(
		[]string{"PD_PARENT_PROCESS=" + strconv.Itoa(os.Getpid())},
		os.Environ(),
		ExtraVariables,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = ExtraFiles

	if err = cmd.Start(); err != nil {
		return fmt.Errorf("failed to start child process: %w", err)
	}

	totalChildren++
	print(
		"Child process %d started with PID %d. Waiting for initialization to "+
			"finish before stopping previous servers",
		totalChildren, cmd.Process.Pid,
	)

	// Wait for the child process to initialize before giving the old process
	// the order to shut down. This allows the old process to keep answering
	// requests until the new process is ready to go
	waitForChildInit()

	// Stop the old child process in the background. We need to wait for the
	// process to stop in a separate thread or else it will turn into a zombie
	stopChild(childProcess)

	// Replace the old child process with the new child process
	childProcess = cmd.Process

	return nil
}

func waitForChildInit() {
	// Make a channel to start listening for SIGUSR1, the signal sent when the
	// child process is done with initialization. When the signal is received,
	// or a timeout is reached, we stop listening
	var initChan = make(chan os.Signal, 1)
	signal.Notify(initChan, StartupFinishedSignal)
	defer signal.Stop(initChan)

	var timer = time.NewTimer(time.Minute * 10)
	defer timer.Stop()

	select {
	case <-timer.C:
		print("Waiting for child process timed out")

	case <-initChan:
		print("Child init finished")
	}
}

func stopChild(child *os.Process) {
	if childProcess == nil {
		return
	}

	shutdownWG.Add(1)
	go func() {
		defer shutdownWG.Done()

		print("Sending stop signal to child process with PID %d", child.Pid)

		if err := child.Signal(syscall.SIGINT); err != nil {
			print("Failed to send SIGINT to child process %d: %s", child.Pid, err)
		}

		state, err := child.Wait()
		print(
			"Child process with PID %d exited with state '%s' and err %v",
			child.Pid, state, err,
		)
	}()
}

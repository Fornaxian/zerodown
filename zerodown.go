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

	// Time to wait for a new process to initialize. When this time has passed
	// the old process will be killed whether the new processes has finished
	// initializing or not.
	StartupTimeout = time.Minute * 10

	// Signals which cause the child process to be restarted
	ReloadSignals = []os.Signal{syscall.SIGHUP}

	// Signals which cause the server to shut down. The first signal in this
	// list is used to shut down the child process when it's time to restart
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

			// Send the stop signal to the child process
			stopChild(childProcess)

			// Wait for processes to shut down
			shutdownWG.Wait()

			print("All child processes ended. Shutdown complete!")
			return true
		} else if inArray(sig, PassthroughSignals) {
			print("Caught signal %s, passing through to child", sig)

			if childProcess != nil {
				if err := childProcess.Signal(sig); err != nil {
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

var (
	// Waitgroup to wait for all child processes to exit before we can exit. If
	// we don't wait for out child processes to end the processes will be
	// disowned and may never stop
	shutdownWG = &sync.WaitGroup{}

	// Stopped is used to notify the waitForChildInit when the child process has
	// unexpectedly ended. This way we avoid having to wait for the timeout when
	// a child process crashes.
	stopped = make(chan int, 1)

	// Total number of times the child process was restarted. This is used for
	// nothing but logging
	restartCounter = 0

	// The old process will be shut down once the new process has finished
	// initialization
	childProcess *os.Process
)

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

	watchChild(cmd.Process)

	// Swap the processes
	var oldProcess = childProcess
	childProcess = cmd.Process

	restartCounter++
	print(
		"Child process %d started with PID %d. Waiting for initialization to "+
			"finish before stopping previous process",
		restartCounter, cmd.Process.Pid,
	)

	// Wait for the child process to initialize before giving the old process
	// the order to shut down. This allows the old process to keep answering
	// requests until the new process is ready to go
	waitForChildInit(cmd.Process.Pid)

	// Now that we know that the new process has finished initializing we can
	// tell the previous process to shut down
	stopChild(oldProcess)

	return nil
}

// watchChild calls Wait on the child process so that the resources are properly
// released when the process ends. If we don't do this the process will turn
// into a zombie when it exits. The process is added to the shutdown waitgroup
// so that the parent process cannot exit until the child process has exited.
func watchChild(child *os.Process) {
	shutdownWG.Add(1)
	go func() {
		state, err := child.Wait()

		print(
			"Child process with PID %d exited with state '%s' and err %v",
			child.Pid, state, err,
		)

		shutdownWG.Done()

		// Send the PID through the stopped channel. If waitForChildInit is
		// waiting this will tell it that the process has ended. This channel is
		// buffered so this goroutine can end when no-one is listening
		stopped <- child.Pid
	}()
}

func waitForChildInit(pid int) {
	// Make a channel to start listening for SIGUSR1, the signal sent when the
	// child process is done with initialization. When the signal is received,
	// or a timeout is reached, we stop listening
	var initChan = make(chan os.Signal, 1)
	signal.Notify(initChan, StartupFinishedSignal)
	defer signal.Stop(initChan)

	var timer = time.NewTimer(StartupTimeout)
	defer timer.Stop()

	for {
		select {
		case <-initChan:
			print("Child init finished")
			return
		case <-timer.C:
			print("Waiting for child process timed out")
			return
		case spid := <-stopped:
			if pid == spid {
				print("Child process %d has crashed before initialization!", pid)
				return
			}
		}
	}
}

func stopChild(child *os.Process) {
	if child == nil {
		return
	}

	print("Sending stop signal to child process with PID %d", child.Pid)

	if err := child.Signal(StopSignals[0]); err != nil {
		print("Failed to send SIGINT to child process %d: %s", child.Pid, err)
	}
}

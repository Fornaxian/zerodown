package zerodown

import (
	"fmt"
	"os"
	"strconv"
)

func IsParent() bool {
	parent, err := strconv.Atoi(os.Getenv("PD_PARENT_PROCESS"))
	if err == nil {
		parentPID = parent
		return false // The parent PID exists, this is a child process
	}
	return true
}

func IsChild() bool {
	return !IsParent()
}

// Restart allows a child process to call for a restart. The parent process will
// start a new child and will shut down the current process
func Restart() (err error) {
	if parentPID != 0 {
		print("Sending %s signal to parent PID %d", ReloadSignals[0], parentPID)

		process, err := os.FindProcess(parentPID)
		if err != nil {
			return fmt.Errorf("could not find parent process: %w", err)
		}
		if err = process.Signal(ReloadSignals[0]); err != nil {
			return fmt.Errorf("could not signal parent process: %w", err)
		}
	} else {
		panic("Restart should not be called on the parent process itself")
	}

	return nil
}

func print(str string, args ...any) {
	if parentPID == 0 {
		Logger.Printf("Zerodown-parent: "+str+"\n", args...)
	} else {
		Logger.Printf("Zerodown-child: "+str+"\n", args...)
	}
}

func inArray[T comparable](needle T, haystack []T) bool {
	for i := range haystack {
		if haystack[i] == needle {
			return true
		}
	}

	return false
}

func combineSlices[T any](slices ...[]T) (result []T) {
	var totLen = 0
	for _, slice := range slices {
		totLen += len(slice)
	}

	result = make([]T, totLen)

	totLen = 0
	for i1 := range slices {
		for i2 := range slices[i1] {
			result[totLen] = slices[i1][i2]
			totLen++
		}
	}

	return result
}

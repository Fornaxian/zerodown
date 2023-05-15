package zerodown

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// PassthroughSockets tells zerodown to pass through all file descriptors to the
// child process. This allows you to use Systemd Socket Activation with zerodown
func PassthroughSockets() {
	// Find files which were passed to us from the parent process. The NewFile
	// function returns nil when a file is not found
	for fd := 3; ; fd++ {
		file := os.NewFile(uintptr(fd), fmt.Sprintf("FILE_%d_FROM_PARENT", fd))
		if file != nil {
			ExtraFiles = append(ExtraFiles, file)
		} else {
			break
		}
	}

	net.FileListener(os.NewFile(3, "MyListener"))
}

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

package launcher

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockFile(file *os.File) (func(), error) {
	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) }, nil
}

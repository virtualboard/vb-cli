//go:build windows

package lock

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func requireSingleLink(file *os.File) error {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	runtime.KeepAlive(file)
	if err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("expected one hard link, found %d", info.NumberOfLinks)
	}
	return nil
}

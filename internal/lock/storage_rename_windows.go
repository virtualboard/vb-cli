//go:build windows

package lock

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func renameStoragePathExclusive(directory *os.File, source, destination string) error {
	parentHandle := windows.Handle(directory.Fd())
	sourceUnicode, err := windows.NewNTUnicodeString(source)
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parentHandle,
		ObjectName:    sourceUnicode,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var sourceHandle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&sourceHandle,
		windows.DELETE|windows.SYNCHRONIZE,
		&attributes,
		&status,
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		return windowsLockNTError(err)
	}
	defer windows.CloseHandle(sourceHandle) // #nosec G104 -- the rename result is authoritative.

	destinationUTF16, err := windows.UTF16FromString(destination)
	if err != nil {
		return err
	}
	if len(destinationUTF16) > windows.MAX_LONG_PATH {
		return windows.ERROR_FILENAME_EXCED_RANGE
	}
	type fileRenameInformationEx struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	nameLength := len(destinationUTF16) - 1
	var extendedHeader fileRenameInformationEx
	extendedBuffer := make([]byte, int(unsafe.Offsetof(extendedHeader.FileName))+nameLength*2)
	// #nosec G103 -- Windows requires this exact variable-length ABI structure.
	extended := (*fileRenameInformationEx)(unsafe.Pointer(&extendedBuffer[0]))
	extended.RootDirectory = parentHandle
	// #nosec G115 -- nameLength is bounded by windows.MAX_LONG_PATH.
	extended.FileNameLength = uint32(nameLength * 2)
	// #nosec G103 -- destination is bounded by windows.MAX_LONG_PATH.
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&extended.FileName[0]))[:nameLength:nameLength], destinationUTF16[:nameLength])
	err = windows.NtSetInformationFile(sourceHandle, &status, &extendedBuffer[0], uint32(len(extendedBuffer)), 65) // #nosec G115 -- bounded buffer.
	if err == nil {
		return nil
	}

	type fileRenameInformation struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	var classicHeader fileRenameInformation
	classicBuffer := make([]byte, int(unsafe.Offsetof(classicHeader.FileName))+nameLength*2)
	// #nosec G103 -- Windows requires this exact variable-length ABI structure.
	classic := (*fileRenameInformation)(unsafe.Pointer(&classicBuffer[0]))
	classic.RootDirectory = parentHandle
	classic.FileNameLength = extended.FileNameLength
	// #nosec G103 -- destination is bounded by windows.MAX_LONG_PATH.
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&classic.FileName[0]))[:nameLength:nameLength], destinationUTF16[:nameLength])
	err = windows.NtSetInformationFile(sourceHandle, &status, &classicBuffer[0], uint32(len(classicBuffer)), windows.FileRenameInformation) // #nosec G115 -- bounded buffer.
	return windowsLockNTError(err)
}

func windowsLockNTError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

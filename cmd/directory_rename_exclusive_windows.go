//go:build windows

package cmd

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func renameDirectoryExclusiveOS(parent *os.File, sourceName, destinationName string) error {
	parentHandle := windows.Handle(parent.Fd())
	sourceUnicode, err := windows.NewNTUnicodeString(sourceName)
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
		return windowsNTError(err)
	}
	defer windows.CloseHandle(sourceHandle)

	destination, err := windows.UTF16FromString(destinationName)
	if err != nil {
		return err
	}
	if len(destination) > windows.MAX_LONG_PATH {
		return windows.ERROR_FILENAME_EXCED_RANGE
	}
	type fileRenameInformationEx struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	nameLength := len(destination) - 1
	var extendedHeader fileRenameInformationEx
	extendedBuffer := make([]byte, int(unsafe.Offsetof(extendedHeader.FileName))+nameLength*2)
	// #nosec G103 -- Windows requires this exact variable-length ABI structure.
	extended := (*fileRenameInformationEx)(unsafe.Pointer(&extendedBuffer[0]))
	extended.RootDirectory = parentHandle
	// #nosec G115 -- nameLength is bounded above by windows.MAX_LONG_PATH.
	extended.FileNameLength = uint32(nameLength * 2)
	// #nosec G103 -- the destination slice is bounded by the fixed Windows maximum above.
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&extended.FileName[0]))[:nameLength:nameLength], destination[:nameLength])
	err = windows.NtSetInformationFile(
		sourceHandle,
		&status,
		&extendedBuffer[0],
		uint32(len(extendedBuffer)), // #nosec G115 -- buffer is bounded by windows.MAX_LONG_PATH.
		65,                          // FileRenameInformationEx
	)
	if err == nil {
		return nil
	}

	// Older filesystems may not implement FileRenameInformationEx. The classic
	// handle-relative structure remains no-replace when ReplaceIfExists is zero.
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
	// #nosec G103 -- the destination slice is bounded by the fixed Windows maximum above.
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&classic.FileName[0]))[:nameLength:nameLength], destination[:nameLength])
	err = windows.NtSetInformationFile(
		sourceHandle,
		&status,
		&classicBuffer[0],
		uint32(len(classicBuffer)), // #nosec G115 -- buffer is bounded by windows.MAX_LONG_PATH.
		windows.FileRenameInformation,
	)
	return windowsNTError(err)
}

func windowsNTError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

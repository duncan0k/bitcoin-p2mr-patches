//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

const nodeName = "bitcoind.exe"

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP       = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleTitleW         = kernel32.NewProc("SetConsoleTitleW")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
)

// setupConsole has the console print UTF-8, so that every language shows, and names the window.
func setupConsole(title string) {
	_, _, _ = procSetConsoleOutputCP.Call(65001)
	if p, err := syscall.UTF16PtrFromString(title); err == nil {
		_, _, _ = procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p)))
	}
}

// systemLanguage is the language of the Windows user interface, if the program has it.
func systemLanguage() string {
	id, _, _ := procGetUserDefaultUILanguage.Call()
	return languageOfLANGID(uint16(id))
}

// hideWindow starts the node with no console window and outside this console's process group: closing this
// window does not kill the node; the program asks it to stop over RPC instead.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
}

// openBrowser opens the page in the default browser.
func openBrowser(url string) error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return exec.Command(filepath.Join(root, "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url).Start()
}

// lockInstance opens the lock file without sharing it, so that a second program on the same data folder
// cannot; the lock goes with the process.
func lockInstance(path string) (func(), error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return func() { _ = syscall.CloseHandle(h) }, nil
}

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

	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP       = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleTitleW         = kernel32.NewProc("SetConsoleTitleW")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
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

// configureNode starts the node with no window and outside this console's process group: closing this window
// does not reach the node; the program asks it to stop over RPC instead.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
}

// openNodeProgram opens the node program shared with readers only: while it is open, nothing can change,
// rename or delete it.
func openNodeProgram(exe string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), exe), nil
}

// jobLimits is JOBOBJECT_EXTENDED_LIMIT_INFORMATION on 64-bit Windows.
type jobLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoInfo                  [6]uint64
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

// The build fails unless jobLimits has the 144 bytes Windows expects.
var _ [144]byte = [unsafe.Sizeof(jobLimits{})]byte{}

// job holds the node. When this program exits, however it exits, Windows closes the program's handle to the job
// and ends the processes in it, unless letFinish has lifted that. Used under node.mu.
var job syscall.Handle

// keepWithProgram puts the node into the job, so that no node is left running hidden after this program.
func keepWithProgram(p *os.Process) error {
	if job == 0 {
		h, _, err := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			return err
		}
		if err := setJobLimits(syscall.Handle(h), jobObjectLimitKillOnJobClose); err != nil {
			_ = syscall.CloseHandle(syscall.Handle(h))
			return err
		}
		job = syscall.Handle(h)
	}
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(job), uintptr(process)); r == 0 {
		return err
	}
	return nil
}

// letFinish lets a node that has agreed to stop finish its shutdown should this program exit first. If Windows
// refuses, the node is ended with the program, which it recovers from at its next start.
func letFinish() {
	if job != 0 {
		_ = setJobLimits(job, 0)
	}
}

func setJobLimits(h syscall.Handle, flags uint32) error {
	limits := jobLimits{LimitFlags: flags}
	r, _, err := procSetInformationJobObject.Call(uintptr(h), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if r == 0 {
		return err
	}
	return nil
}

// terminate ends the node at once: Windows has no signal to ask a program without a console to stop.
func terminate(p *os.Process) error {
	return p.Kill()
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

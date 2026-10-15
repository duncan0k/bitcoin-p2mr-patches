//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const nodeName = "bitcoind.exe"

const shutdownTimeout = 4 * time.Second
const endUnresponsiveNode = true

func defaultDataHome() (string, error)             { return os.UserCacheDir() }
func stoppingSignals() []os.Signal                 { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
func ignoreBrokenPipe()                            {}
func stopLeftoverNode(string, string) error        { return nil }
func removeNodePID(string) error                   { return nil }
func prepareDataFolder(dir string) (string, error) { return dir, os.MkdirAll(dir, 0o700) }
func dataPathProblem(string) error                 { return nil }
func checkSignetFolder(string) error               { return nil }
func checkDataTree(string) error                   { return nil }

func readCookie(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errNotReady
	}
	return data, nil
}

// Windows ends a program five seconds after its console window was closed.
func shutdownNode(n *node, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	n.stop()
	n.wait(time.Until(deadline))
}

// startNode holds the checked image against changes, starts it suspended, and admits only that very file.
func (n *node) startNode(cmd *exec.Cmd) error {
	f, err := openNodeProgram(n.exe)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := checkSHA256(f, n.exeSHA256); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := admit(cmd.Process, f); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
	createSuspended       = 0x00000004

	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
	processQueryLimitedInformation    = 0x1000
	threadSuspendResume               = 0x0002
	fileIDInfoClass                   = 18 // FileIdInfo
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP       = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleTitleW         = kernel32.NewProc("SetConsoleTitleW")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procThread32First            = kernel32.NewProc("Thread32First")
	procThread32Next             = kernel32.NewProc("Thread32Next")
	procOpenThread               = kernel32.NewProc("OpenThread")
	procResumeThread             = kernel32.NewProc("ResumeThread")
	procQueryFullProcessImageW   = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetFileInformationByEx   = kernel32.NewProc("GetFileInformationByHandleEx")
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

// configureNode starts the node with no window, outside this console's process group, and suspended, until
// admit lets it run. Closing this window does not reach the node; the program asks it to
// stop over RPC instead.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup | createSuspended}
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
// and ends the processes in it. Used under node.mu.
var job syscall.Handle

// admit puts the node, started suspended, into the job, so that no node is left running hidden after this
// program; checks that it is the very file this program checked and holds open; and only then lets it run. Should
// this program die before the node is in the job, what is left is a suspended process that has run nothing.
func admit(p *os.Process, program *os.File) error {
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
	process, err := syscall.OpenProcess(processSetQuota|processTerminate|processQueryLimitedInformation, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(job), uintptr(process)); r == 0 {
		return err
	}
	same, err := runsFile(process, program)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("the node started is not the node program that was checked")
	}
	return resume(uint32(p.Pid))
}

// runsFile reports whether process runs program: the file Windows loaded for it and program are one file of one
// volume, whatever the paths that led to them. Without ids to compare, it reports an error.
func runsFile(process syscall.Handle, program *os.File) (bool, error) {
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if r, _, err := procQueryFullProcessImageW.Call(uintptr(process), 0, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size))); r == 0 {
		return false, err
	}
	image, err := syscall.CreateFile(&buf[0], 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false, err
	}
	defer syscall.CloseHandle(image)
	a, err := idOf(image)
	if err != nil {
		return false, err
	}
	b, err := idOf(syscall.Handle(program.Fd()))
	if err != nil {
		return false, err
	}
	return a == b, nil
}

// fileID is FILE_ID_INFO: the volume's serial number and the file's 128-bit id, which, unlike the 64-bit index
// of GetFileInformationByHandle, identifies a file on ReFS as well.
type fileID struct {
	VolumeSerialNumber uint64
	ID                 [16]byte
}

// The build fails unless fileID has the 24 bytes Windows expects.
var _ [24]byte = [unsafe.Sizeof(fileID{})]byte{}

func idOf(h syscall.Handle) (fileID, error) {
	var id fileID
	if r, _, err := procGetFileInformationByEx.Call(uintptr(h), fileIDInfoClass, uintptr(unsafe.Pointer(&id)),
		unsafe.Sizeof(id)); r == 0 {
		return id, err
	}
	return id, id.check()
}

// check refuses an id the file system left empty: one without 128-bit file ids sets the id to zero, whatever the
// volume's serial number, and two of its files would then look like one.
func (id fileID) check() error {
	if id.ID == ([16]byte{}) {
		return errors.New("the file system gives the node program no id")
	}
	return nil
}

// threadEntry is THREADENTRY32.
type threadEntry struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePriority   int32
	DeltaPriority  int32
	Flags          uint32
}

// resume lets a process started suspended run, by resuming its thread, the only one it has by then.
func resume(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	resumed := 0
	more, _, _ := procThread32First.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	for ; more != 0; more, _, _ = procThread32Next.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry))) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, _, err := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
		if thread == 0 {
			return err
		}
		count, _, err := procResumeThread.Call(thread)
		_ = syscall.CloseHandle(syscall.Handle(thread))
		if uint32(count) == 0xffffffff {
			return err
		}
		resumed++
	}
	if resumed == 0 {
		return errors.New("the node has no thread to resume")
	}
	return nil
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

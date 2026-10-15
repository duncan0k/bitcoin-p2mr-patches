// Command Ark0Witness runs an Ark-0 node on this computer and shows, on a local web page, what that node
// itself sees of the M3 drill: whether it is on Ark-0, whether block 30000 has passed, the state of the two
// tripwires, and how it judges a raw transaction pasted into the page. It never sends a transaction anywhere.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// version and bitcoindSHA256 are set at build time with -ldflags -X (see build.sh). A build without
// bitcoindSHA256 runs whatever bitcoind it finds next to it, which only development builds should do.
var (
	version            = "dev"
	bitcoindSHA256     = ""
	errInstanceRunning = errors.New("another witness instance is running")
)

func main() {
	os.Exit(run())
}

func run() int {
	// Terminal closure (SIGHUP on Unix, CTRL_CLOSE_EVENT as SIGTERM on Windows) and Ctrl+C are caught from the
	// start, so that a node is stopped or never started.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, stoppingSignals()...)
	ignoreBrokenPipe()

	dataFlag := flag.String("datadir", "", "data folder (default: Ark0Witness in the local application data folder)")
	nodeFlag := flag.String("bitcoind", "", "node program (default: bitcoind next to this program)")
	noBrowser := flag.Bool("no-browser", false, "do not open the page in a browser")
	langFlag := flag.String("lang", "", "language of this window: en, zh, ja, ko, ru or es")
	flag.Parse()

	t := fillConsole(consoleTexts[pickLanguage(*langFlag, systemLanguage())], runtime.GOOS)
	setupConsole(t.Title)
	fmt.Printf("%s %s\n%s\n\n", t.Title, version, t.Tagline)
	fail := func(message string) int { return failed(t, message, signals) }

	exe, err := nodeProgram(*nodeFlag)
	if err != nil {
		return fail(t.MissingNode)
	}
	if err := checkNodeProgram(exe, bitcoindSHA256); err != nil {
		if runtime.GOOS != "windows" {
			return fail(t.WrongNode + "\n" + err.Error())
		}
		return fail(t.WrongNode)
	}
	dataDir, err := dataFolder(*dataFlag)
	if err != nil {
		return fail(fmt.Sprintf(t.NoDataFolder, err))
	}
	release, err := lockInstance(filepath.Join(dataDir, lockFile))
	if err != nil {
		if runtime.GOOS != "windows" && !errors.Is(err, errInstanceRunning) {
			return fail(fmt.Sprintf(t.StartFailed, err))
		}
		// Another window already runs the witness on this data folder: show its page rather than start a second node.
		url, pageErr := readPageURL(dataDir)
		if pageErr == nil {
			fmt.Printf("%s\n%s\n", t.Already, url)
			if !*noBrowser {
				_ = openBrowser(url)
			}
			select {
			case <-signals:
			case <-time.After(5 * time.Second):
			}
			return 0
		}
		if runtime.GOOS != "windows" {
			return fail(t.Busy + "\n" + pageErr.Error())
		}
		return fail(t.Busy)
	}
	defer release()
	if err := stopLeftoverNode(dataDir, t.Leftover); err != nil {
		return fail(fmt.Sprintf(t.StartFailed, err))
	}
	if err := checkDataTree(dataDir); err != nil {
		return fail(fmt.Sprintf(t.StartFailed, err))
	}

	n := newNode(exe, bitcoindSHA256, dataDir)
	srv, err := newServer(n, dataDir)
	if err != nil {
		return fail(fmt.Sprintf(t.StartFailed, err))
	}
	n.start()
	url := srv.url()
	pagePath := filepath.Join(dataDir, pageFile)
	_ = writePageFile(pagePath, []byte(url+"\n"))
	defer os.Remove(pagePath)
	attempted := shouldOpenBrowser(runtime.GOOS, *noBrowser, os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY"))
	var browserErr error
	if attempted {
		browserErr = openBrowser(url)
	}
	fmt.Printf("%s\n%s\n\n%s\n", runningText(t, attempted, browserErr), url, t.KeepOpen)

	select {
	case <-signals:
		fmt.Println(t.Stopping)
		shutdownNode(n, shutdownTimeout)
		return 0
	case <-n.done:
		_, message := n.state()
		return fail(strings.TrimSpace(t.Stopped + "\n" + message))
	}
}

// failed shows a message and keeps the window open until Enter, Ctrl+C or the window is closed, so that a
// double-clicked program does not vanish before its message can be read.
func failed(t console, message string, signals <-chan os.Signal) int {
	fmt.Printf("\n%s\n\n%s\n", message, t.PressEnter)
	enter := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		close(enter)
	}()
	select {
	case <-enter:
	case <-signals:
	}
	return 1
}

// nodeProgram is the node to run: the one given, or bitcoind in this program's own folder. It is missing
// when the program was started from inside the zip, before the whole folder was unpacked.
func nodeProgram(given string) (string, error) {
	exe := given
	if exe == "" {
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		exe = filepath.Join(filepath.Dir(self), nodeName)
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("the node program is a folder")
	}
	// The file checked must be the file started: on Windows, exec would run name.exe for a name without it.
	if found, err := exec.LookPath(exe); err != nil || found != exe {
		return "", errors.New("the node program is not an executable file")
	}
	return exe, nil
}

// checkNodeProgram compares the node program with the SHA-256 this program was built with, if any.
func checkNodeProgram(exe, want string) error {
	f, err := openNodeProgram(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	return checkSHA256(f, want)
}

// checkSHA256 compares what r holds with the SHA-256 want, if any.
func checkSHA256(r io.Reader, want string) error {
	if want == "" {
		return nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return errors.New("the node program is not the one this witness was built with")
	}
	return nil
}

// dataFolder creates the folder given, or the platform's persistent application data folder, privately.
func dataFolder(given string) (string, error) {
	dir := given
	if dir == "" {
		base, err := defaultDataHome()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "Ark0Witness")
	}
	if err := dataPathProblem(dir); err != nil {
		return "", err
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return prepareDataFolder(dir)
}

func shouldOpenBrowser(platform string, noBrowser bool, display, wayland string) bool {
	return !noBrowser && (platform != "linux" || display != "" || wayland != "")
}

func runningText(t console, attempted bool, err error) string {
	if attempted && err == nil {
		return t.Running
	}
	return t.RunningNoBrowser
}

var pageURLPattern = regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]{1,5}/[0-9a-f]{64}/$`)

// readPageURL is the page of the window that already runs on this data folder, if its file holds one.
func readPageURL(dataDir string) (string, error) {
	path := filepath.Join(dataDir, pageFile)
	data, err := readPageFile(path)
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(string(data))
	if !pageURLPattern.MatchString(url) {
		return "", fmt.Errorf("page file %q does not contain a valid witness page URL", path)
	}
	return url, nil
}

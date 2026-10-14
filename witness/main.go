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
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// version and bitcoindSHA256 are set at build time with -ldflags -X (see build.sh). A build without
// bitcoindSHA256 runs whatever bitcoind it finds next to it, which only development builds should do.
var (
	version        = "dev"
	bitcoindSHA256 = ""
)

func main() {
	os.Exit(run())
}

func run() int {
	dataFlag := flag.String("datadir", "", "data folder (default: Ark0Witness in the local application data folder)")
	nodeFlag := flag.String("bitcoind", "", "node program (default: bitcoind next to this program)")
	noBrowser := flag.Bool("no-browser", false, "do not open the page in a browser")
	langFlag := flag.String("lang", "", "language of this window: en, zh, ja, ko, ru or es")
	flag.Parse()

	t := consoleTexts[pickLanguage(*langFlag, systemLanguage())]
	setupConsole(t.Title)
	fmt.Printf("%s %s\n%s\n\n", t.Title, version, t.Tagline)

	exe, err := nodeProgram(*nodeFlag)
	if err != nil {
		return fail(t, t.MissingNode)
	}
	if err := checkNodeProgram(exe, bitcoindSHA256); err != nil {
		return fail(t, t.WrongNode)
	}
	dataDir, err := dataFolder(*dataFlag)
	if err != nil {
		return fail(t, fmt.Sprintf(t.NoDataFolder, err))
	}
	release, err := lockInstance(filepath.Join(dataDir, lockFile))
	if err != nil {
		// Another window already runs the witness on this data folder: show its page rather than start a second node.
		if url, ok := readPageURL(dataDir); ok {
			fmt.Printf("%s\n%s\n", t.Already, url)
			if !*noBrowser {
				_ = openBrowser(url)
			}
			time.Sleep(5 * time.Second)
			return 0
		}
		return fail(t, t.Busy)
	}
	defer release()

	n := newNode(exe, dataDir)
	n.start()
	srv, err := newServer(n, dataDir)
	if err != nil {
		n.stop()
		return fail(t, fmt.Sprintf(t.StartFailed, err))
	}
	url := srv.url()
	pagePath := filepath.Join(dataDir, pageFile)
	_ = os.WriteFile(pagePath, []byte(url+"\n"), 0o600)
	defer os.Remove(pagePath)
	fmt.Printf("%s\n%s\n\n%s\n", t.Running, url, t.KeepOpen)
	if !*noBrowser {
		_ = openBrowser(url)
	}

	// Closing the console window arrives as SIGTERM (CTRL_CLOSE_EVENT on Windows), Ctrl+C as an interrupt.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
		fmt.Println(t.Stopping)
		n.stop()
		n.wait(2 * time.Second)
		return 0
	case <-n.done:
		_, message := n.state()
		return fail(t, fmt.Sprintf(t.Stopped, message))
	}
}

// fail shows a message and keeps the window open until Enter, so that a double-clicked program does not
// vanish before its message can be read.
func fail(t console, message string) int {
	fmt.Printf("\n%s\n\n%s\n", message, t.PressEnter)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
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
	if !info.Mode().IsRegular() {
		return "", errors.New("the node program is not a file")
	}
	return exe, nil
}

// checkNodeProgram compares the node program with the SHA-256 this program was built with, if any.
func checkNodeProgram(exe, want string) error {
	if want == "" {
		return nil
	}
	f, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return errors.New("the node program is not the one this witness was built with")
	}
	return nil
}

// dataFolder is the folder given, or Ark0Witness in the local application data folder (%LOCALAPPDATA% on
// Windows); it is created if need be, readable by this user only.
func dataFolder(given string) (string, error) {
	dir := given
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "Ark0Witness")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o700)
}

var pageURLPattern = regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]{1,5}/[0-9a-f]{64}/$`)

// readPageURL is the page of the window that already runs on this data folder, if its file holds one.
func readPageURL(dataDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dataDir, pageFile))
	if err != nil {
		return "", false
	}
	url := strings.TrimSpace(string(data))
	return url, pageURLPattern.MatchString(url)
}

package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// pageListener is used only on Linux, where browser command lines can expose the page URL.
type pageListener struct {
	net.Listener
	uid uint32
}

func (l *pageListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		// Inspect kernel metadata only: a client that sends no data must not hold up Accept.
		data, err := os.ReadFile("/proc/net/tcp")
		if err == nil {
			client, server := procTCPAddress(conn.RemoteAddr()), procTCPAddress(conn.LocalAddr())
			first := procTCPClient(string(data), client, server, l.uid)
			// procfs reads state before uid and inode, which can become zero when a socket is orphaned.
			// close moves it out of ESTABLISHED before orphaning it, so an independent read after parsing
			// cannot show that closing socket as ESTABLISHED again. Require the same uid and nonzero inode.
			if first.inode != 0 {
				data, err = os.ReadFile("/proc/net/tcp")
				if err == nil && procTCPAllows(first, procTCPClient(string(data), client, server, l.uid)) {
					return conn, nil
				}
			}
		}
		_ = conn.Close()
	}
}

func procTCPAddress(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.IP.To4() == nil || tcp.Port < 0 || tcp.Port > 65535 {
		return ""
	}
	return fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(tcp.IP.To4()), tcp.Port)
}

type procTCPPeer struct {
	uid   uint32
	inode uint64
}

func procTCPAllows(first, second procTCPPeer) bool {
	return first.inode != 0 && first == second
}

// procTCPClient looks for the client's socket, whose endpoints reverse the accepted socket's endpoints.
func procTCPClient(text, client, server string, uid uint32) procTCPPeer {
	if !procTCPHexPair(client, 8, 4) || !procTCPHexPair(server, 8, 4) {
		return procTCPPeer{}
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	if !scanner.Scan() {
		return procTCPPeer{}
	}
	header := strings.Fields(scanner.Text())
	if len(header) < 12 || header[0] != "sl" || header[1] != "local_address" ||
		header[2] != "rem_address" || header[3] != "st" || header[9] != "uid" || header[11] != "inode" {
		return procTCPPeer{}
	}
	var peer procTCPPeer
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || !strings.HasSuffix(fields[0], ":") ||
			!procTCPHexPair(fields[1], 8, 4) || !procTCPHexPair(fields[2], 8, 4) ||
			len(fields[3]) != 2 || !procTCPHexPair(fields[4], 8, 8) || !procTCPHexPair(fields[5], 2, 8) {
			return procTCPPeer{}
		}
		for _, number := range []struct {
			value string
			base  int
			bits  int
		}{
			{strings.TrimSuffix(fields[0], ":"), 10, 32},
			{fields[3], 16, 8}, {fields[6], 16, 32}, {fields[7], 10, 32},
			{fields[8], 10, 32}, {fields[9], 10, 64},
		} {
			if _, err := strconv.ParseUint(number.value, number.base, number.bits); err != nil {
				return procTCPPeer{}
			}
		}
		if fields[1] == client && fields[2] == server {
			owner, _ := strconv.ParseUint(fields[7], 10, 32)
			inode, _ := strconv.ParseUint(fields[9], 10, 64)
			if fields[3] != "01" || owner != uint64(uid) && owner != 0 || inode == 0 {
				return procTCPPeer{}
			}
			peer = procTCPPeer{uid: uint32(owner), inode: inode}
		}
	}
	if scanner.Err() != nil {
		return procTCPPeer{}
	}
	return peer
}

func procTCPHexPair(value string, left, right int) bool {
	if len(value) != left+1+right || value[left] != ':' {
		return false
	}
	_, leftErr := strconv.ParseUint(value[:left], 16, left*4)
	_, rightErr := strconv.ParseUint(value[left+1:], 16, right*4)
	return leftErr == nil && rightErr == nil
}

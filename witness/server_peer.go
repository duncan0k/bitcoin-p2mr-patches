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
		if err == nil && procTCPAllows(string(data), procTCPAddress(conn.RemoteAddr()), procTCPAddress(conn.LocalAddr()), l.uid) {
			return conn, nil
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

// procTCPAllows looks for the client's socket, whose endpoints reverse the accepted socket's endpoints.
func procTCPAllows(text, client, server string, uid uint32) bool {
	if !procTCPHexPair(client, 8, 4) || !procTCPHexPair(server, 8, 4) {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	if !scanner.Scan() {
		return false
	}
	header := strings.Fields(scanner.Text())
	if len(header) < 12 || header[0] != "sl" || header[1] != "local_address" ||
		header[2] != "rem_address" || header[3] != "st" || header[9] != "uid" {
		return false
	}
	allowed := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || !strings.HasSuffix(fields[0], ":") ||
			!procTCPHexPair(fields[1], 8, 4) || !procTCPHexPair(fields[2], 8, 4) ||
			len(fields[3]) != 2 || !procTCPHexPair(fields[4], 8, 8) || !procTCPHexPair(fields[5], 2, 8) {
			return false
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
				return false
			}
		}
		if fields[1] == client && fields[2] == server {
			owner, _ := strconv.ParseUint(fields[7], 10, 32)
			// Only ESTABLISHED counts: closing before this check can leave an orphaned socket in
			// FIN_WAIT2 or TIME_WAIT with uid 0, which must not authorize a different user's client.
			if fields[3] != "01" || owner != uint64(uid) && owner != 0 {
				return false
			}
			allowed = true
		}
	}
	return allowed && scanner.Err() == nil
}

func procTCPHexPair(value string, left, right int) bool {
	if len(value) != left+1+right || value[left] != ':' {
		return false
	}
	_, leftErr := strconv.ParseUint(value[:left], 16, left*4)
	_, rightErr := strconv.ParseUint(value[left+1:], 16, right*4)
	return leftErr == nil && rightErr == nil
}

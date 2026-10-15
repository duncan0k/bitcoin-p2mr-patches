package main

import (
	"net"
	"strings"
	"testing"
)

func TestProcTCPClient(t *testing.T) {
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	const client = "0100007F:C350"
	const server = "0100007F:9C40"
	const entry = "  1: 0100007F:C350 0100007F:9C40 01 00000000:00000000 00:00000000 00000000 1000 0 12345 1 0000000000000000 20 4 30 10 -1\n"
	const reverse = "  2: 0100007F:9C40 0100007F:C350 01 00000000:00000000 00:00000000 00000000 1000 0 12346 1 0000000000000000 20 4 30 10 -1\n"
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"same user established", header + entry, true},
		{"root established", header + strings.Replace(entry, " 1000 ", " 0 ", 1), true},
		{"another user", header + strings.Replace(entry, " 1000 ", " 1001 ", 1), false},
		{"root fin wait 2", header + strings.Replace(strings.Replace(entry, " 1000 ", " 0 ", 1), " 01 ", " 05 ", 1), false},
		{"root time wait", header + strings.Replace(strings.Replace(entry, " 1000 ", " 0 ", 1), " 01 ", " 06 ", 1), false},
		{"same user closing", header + strings.Replace(entry, " 01 ", " 08 ", 1), false},
		{"no entry", header, false},
		{"server socket only", header + reverse, false},
		{"both sockets", header + reverse + entry, true},
		{"server cannot authorize client", header + reverse + strings.Replace(entry, " 1000 ", " 1001 ", 1), false},
		{"empty", "", false},
		{"missing header", entry, false},
		{"bad header", "invalid\n" + entry, false},
		{"short row", header + "1: 0100007F:C350\n", false},
		{"bad address", header + strings.Replace(entry, "0100007F", "010000GG", 1), false},
		{"bad port", header + strings.Replace(entry, "C350", "ZZZZ", 1), false},
		{"bad state", header + strings.Replace(entry, " 01 ", " GG ", 1), false},
		{"bad uid", header + strings.Replace(entry, " 1000 ", " user ", 1), false},
		{"bad queue", header + strings.Replace(entry, "00000000:00000000", "invalid", 1), false},
		{"bad inode", header + strings.Replace(entry, "12345", "invalid", 1), false},
		{"zero inode", header + strings.Replace(entry, "12345", "0", 1), false},
		{"malformed after match", header + entry + "invalid\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := procTCPClient(c.text, client, server, 1000).inode != 0; got != c.want {
				t.Fatalf("allowed = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProcTCPAllows(t *testing.T) {
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	const entry = "  1: 0100007F:C350 0100007F:9C40 01 00000000:00000000 00:00000000 00000000 1000 0 12345\n"
	root := strings.Replace(entry, " 1000 ", " 0 ", 1)
	zeroInode := strings.Replace(entry, "12345", "0", 1)
	for _, c := range []struct {
		name, first, second string
		want                bool
	}{
		{"agreeing user", entry, entry, true},
		{"agreeing root", root, root, true},
		{"second closing", entry, strings.Replace(entry, " 01 ", " 04 ", 1), false},
		{"second root", entry, root, false},
		{"second user", root, entry, false},
		{"second foreign uid", entry, strings.Replace(entry, " 1000 ", " 1001 ", 1), false},
		{"second zero inode", entry, zeroInode, false},
		{"second different inode", entry, strings.Replace(entry, "12345", "12346", 1), false},
		{"both zero inode", zeroInode, zeroInode, false},
		{"closing race", root, strings.Replace(root, " 01 ", " 04 ", 1), false},
		{"second missing", entry, "", false},
		{"second malformed", entry, "invalid\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			first := procTCPClient(header+c.first, "0100007F:C350", "0100007F:9C40", 1000)
			second := procTCPClient(header+c.second, "0100007F:C350", "0100007F:9C40", 1000)
			if got := procTCPAllows(first, second); got != c.want {
				t.Fatalf("allowed = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProcTCPAddress(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000}
	if got := procTCPAddress(addr); got != "0100007F:C350" {
		t.Fatalf("address = %q", got)
	}
	if got := procTCPAddress(&net.TCPAddr{IP: net.ParseIP("::1"), Port: 50000}); got != "" {
		t.Fatalf("IPv6 address accepted: %q", got)
	}
}

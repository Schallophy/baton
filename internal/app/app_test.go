package app

import (
	"net"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"localhost":   true,
		"::1":         true,
		"LOCALHOST":   true, // DNS names are case-insensitive; it still binds loopback
		"0.0.0.0":     false,
		"192.168.1.5": false,
		"":            false,
		"[::1]":       false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Fatalf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestListenFallsBackWhenPortIsTaken(t *testing.T) {
	first, firstAddr, err := listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer first.Close()

	port := portOf(t, firstAddr)
	// 端口已被占：显式指定时不能让用户看不到界面，必须顺延。
	second, _, err := listen("127.0.0.1", port)
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	defer second.Close()
	if got := portOf(t, second.Addr().String()); got == port {
		t.Fatalf("fell back onto the busy port %d", port)
	}
}

func TestPortOf(t *testing.T) {
	if got := portOf(t, "127.0.0.1:7841"); got != 7841 {
		t.Fatalf("portOf = %d", got)
	}
	if got := portOf(t, "[::1]:7841"); got != 7841 {
		t.Fatalf("portOf v6 = %d", got)
	}
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split %q: %v", address, err)
	}
	value := 0
	for _, char := range strings.TrimSpace(port) {
		if char < '0' || char > '9' {
			t.Fatalf("non-numeric port %q", port)
		}
		value = value*10 + int(char-'0')
	}
	return value
}

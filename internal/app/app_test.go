package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

	port := portOf(firstAddr)
	if port == 0 {
		t.Fatalf("portOf(%q) = 0", firstAddr)
	}
	// 端口已被占：显式指定时不能让用户看不到界面，必须顺延。
	second, secondAddr, err := listen("127.0.0.1", port)
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	defer second.Close()
	if got := portOf(secondAddr); got == port {
		t.Fatalf("fell back onto the busy port %d", port)
	}
}

func TestPortOf(t *testing.T) {
	cases := map[string]int{
		"127.0.0.1:7841": 7841,
		"[::1]:7841":     7841,
		"127.0.0.1:0":    0,
		"nonsense":       0,
	}
	for address, want := range cases {
		if got := portOf(address); got != want {
			t.Fatalf("portOf(%q) = %d, want %d", address, got, want)
		}
	}
}

// homeForState points stateDir at a temp directory so the tests never touch the
// user's real ~/.baton.
func homeForState(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
}

func TestStateRoundTrip(t *testing.T) {
	homeForState(t)
	state := serverState{Address: "http://127.0.0.1:7841", Port: 7841, PID: 1234, Version: "0.1.0", Token: "abc"}
	if err := writeState(state); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	got, ok := readState()
	if !ok {
		t.Fatalf("readState found nothing")
	}
	if got != state {
		t.Fatalf("got %+v, want %+v", got, state)
	}
	// 写出来的是权限收紧的文件：里面有一个能让进程退出的令牌。
	// Windows 上 os.WriteFile 不套用 Unix 权限位，所以只在别的平台上检查。
	if runtime.GOOS != "windows" {
		info, err := os.Stat(statePath())
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("state file is readable by others: %v", info.Mode())
		}
	}
}

func TestStaleStateIsDiscarded(t *testing.T) {
	homeForState(t)
	// 进程被强杀时状态文件会留下来；不能因为它存在就当成还活着。
	if err := writeState(serverState{Address: "http://127.0.0.1:1", Version: "0.1.0"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if _, ok := liveInstance(); ok {
		t.Fatalf("a dead instance was reported as running")
	}
	if _, ok := readState(); ok {
		t.Fatalf("stale state was not cleared")
	}
}

func TestProbeMetaChecksVersion(t *testing.T) {
	// 端口上可能有别的程序；只有版本对得上才是 baton。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/meta" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "something-else"})
	}))
	defer server.Close()
	if probeMeta(server.URL) {
		t.Fatalf("a foreign service was accepted as baton")
	}

	ours := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"version": version})
	}))
	defer ours.Close()
	if !probeMeta(ours.URL) {
		t.Fatalf("our own server was not recognised")
	}
}

func TestRequestShutdownRejectsBadToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-baton-token") != "good" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	if err := requestShutdown(server.URL, "bad", 0); err == nil {
		t.Fatalf("bad token was accepted")
	}
	if err := requestShutdown(server.URL, "good", 0); err != nil {
		t.Fatalf("good token rejected: %v", err)
	}
}

func TestStatePathIsUnderHome(t *testing.T) {
	homeForState(t)
	given := statePath()
	if filepath.Base(given) != "server.json" {
		t.Fatalf("statePath = %q", given)
	}
	if filepath.Dir(given) != filepath.Join(os.Getenv("USERPROFILE"), ".baton") {
		t.Fatalf("state dir = %q", filepath.Dir(given))
	}
}

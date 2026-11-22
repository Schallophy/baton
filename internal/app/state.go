package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// serverState is what one running baton writes down so the next invocation can
// find it instead of starting a second server on a second port.
type serverState struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	// Token lets `baton stop` ask the running instance to shut down. 网页无法
	// 伪造它：跨站请求带不了自定义头（会先触发预检，而 baton 不返回 CORS 头）。
	Token string `json:"token"`
}

// stateDir is where the running instance leaves its address. It lives under the
// user's home rather than the build directory so `go install` and a copied
// binary agree on the same place.
func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".baton")
}

func statePath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "server.json")
}

func newToken() string {
	// 16 字节足够：它的作用是区分「本机的 baton 命令」和「浏览器里的页面」。
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

func writeState(state serverState) error {
	path := statePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// 先写临时文件再改名：读的一方永远看不到写了一半的 JSON。
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func readState() (serverState, bool) {
	path := statePath()
	if path == "" {
		return serverState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return serverState{}, false
	}
	var state serverState
	if err := json.Unmarshal(data, &state); err != nil || state.Address == "" {
		return serverState{}, false
	}
	return state, true
}

func clearState() {
	if path := statePath(); path != "" {
		_ = os.Remove(path)
	}
}

// liveInstance returns the recorded server if something is actually answering
// there. The state file can outlive a killed process, so a stale record must not
// be trusted: the only proof of life is a reply from /api/meta.
func liveInstance() (serverState, bool) {
	state, ok := readState()
	if !ok {
		return serverState{}, false
	}
	if !probeMeta(state.Address) {
		clearState()
		return serverState{}, false
	}
	return state, true
}

func probeMeta(address string) bool {
	client := &http.Client{Timeout: 600 * time.Millisecond}
	response, err := client.Get(address + "/api/meta")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return false
	}
	// 端口上可能是别的程序；只有版本对得上才是 baton。
	return payload.Version == version
}

// stopInstance asks a running baton to exit. It prefers the graceful path (the
// server shuts down and clears its own state). A recorded instance that predates
// the shutdown endpoint, or one that stopped answering, falls back to killing the
// recorded PID so `baton stop` still works.
func stopInstance(state serverState, timeout time.Duration) error {
	if state.Token != "" {
		if err := requestShutdown(state.Address, state.Token, timeout); err == nil {
			return nil
		}
	}
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("无法结束进程 %d：%w", state.PID, err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("无法结束进程 %d：%w", state.PID, err)
	}
	return nil
}

func requestShutdown(address, token string, timeout time.Duration) error {
	body := bytes.NewReader([]byte("{}"))
	request, err := http.NewRequest("POST", address+"/api/shutdown", body)
	if err != nil {
		return err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-baton-token", token)
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("实例拒绝退出：" + response.Status)
	}
	return nil
}

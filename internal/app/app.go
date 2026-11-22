// Package app wires the CLI, the local HTTP server and the embedded UI into one
// process.
//
// The default action is the UI: you are in an escape hatch, and a list of
// sessions with copy buttons is the shortest path to being running again. The
// CLI exists so the same detectors can be scripted (and later exposed over MCP)
// without a browser in the loop.
package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Schallophy/baton/internal/httpapi"
	"github.com/Schallophy/baton/internal/sessions"
)

const version = httpapi.Version

// Run parses argv and returns the process exit code.
func Run() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve", "ui":
			return serve(os.Args[2:])
		case "list":
			return listCmd(os.Args[2:])
		case "handoff", "copy":
			return handoffCmd(os.Args[2:])
		case "version", "--version", "-v":
			fmt.Println("baton", version)
			return 0
		case "help", "--help", "-h":
			usage()
			return 0
		default:
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "baton: 未知命令 %q\n\n", os.Args[1])
				usage()
				return 2
			}
		}
	}
	return serve(nil)
}

func usage() {
	fmt.Fprintf(os.Stderr, `baton %s — 把卡住的编码代理会话交接给下一个

用法：
  baton                          打开界面（默认）
  baton serve [--port N] [--no-open]
                                 启动本地界面
  baton list [--json]            列出会话；卡住的排最前
  baton handoff --agent --id     直接打印交接文本
  baton handoff --latest         接住最近一个卡住的会话
  baton version

交接选项：
  --note TEXT    给下一个 agent 的一句话（你为什么换人、接下来做什么）
  --reason CODE  quota / upstream / aborted / switch / other
  --full         带上压缩前的完整对话
  --limit N      对话块的字符上限（默认 40000，0 = 不限）
  --no-summary / --no-messages / --no-todos
  --json         输出结构化结果
`, version)
}

// ---- serve ---------------------------------------------------------------

func serve(args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := flags.Int("port", 0, "监听端口（0 = 自动挑选）")
	host := flags.String("host", "127.0.0.1", "监听地址")
	noOpen := flags.Bool("no-open", false, "不自动打开浏览器")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	// 非 loopback 地址等于把本机会话库暴露给网络；baton 没有认证层，所以
	// 直接拒绝，而不是提供一个「能连但危险」的选项。
	if !isLoopbackHost(*host) {
		fmt.Fprintln(os.Stderr, "baton: 只允许监听本机地址（127.0.0.1 / ::1 / localhost）")
		return 2
	}

	detector := sessions.NewDetector()
	server := httpapi.NewServer(detector, version)

	listener, actual, err := listen(*host, *port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "baton: 无法监听端口："+err.Error())
		return 1
	}

	address := fmt.Sprintf("http://%s", actual)
	httpServer := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "baton: "+err.Error())
		}
	}()

	fmt.Printf("baton %s\n  界面  %s\n  按 Ctrl+C 退出\n", version, address)
	if !*noOpen {
		openBrowser(address)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	<-done
	return 0
}

// listen binds the requested port, or asks the OS for a free one. A port that is
// already taken must not stop the user from getting to their sessions, so an
// explicit port falls back to automatic selection instead of failing.
func listen(host string, port int) (net.Listener, string, error) {
	if port > 0 {
		if listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			return listener, listener.Addr().String(), nil
		} else if !isPortBusy(err) {
			return nil, "", err
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, "", err
	}
	return listener, listener.Addr().String(), nil
}

func isPortBusy(err error) bool {
	return strings.Contains(err.Error(), "already in use") ||
		strings.Contains(err.Error(), "Only one usage")
}

// isLoopbackHost reports whether a listen address keeps the server on this
// machine. Empty means "all interfaces" to net.Listen, which is not acceptable.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.IsLoopback()
	}
	return false
}

func openBrowser(address string) {
	var command string
	var args []string
	switch runtime.GOOS {
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", address}
	case "darwin":
		command, args = "open", []string{address}
	default:
		command, args = "xdg-open", []string{address}
	}
	_ = exec.Command(command, args...).Start()
}

// ---- list ----------------------------------------------------------------

func listCmd(args []string) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "输出 JSON")
	stuckOnly := flags.Bool("stuck", false, "只列卡住的会话")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	detector := sessions.NewDetector()
	result := detector.Refresh()

	if *asJSON {
		encode(result)
		return 0
	}

	if len(result.Sessions) == 0 {
		fmt.Println("没有找到会话。")
		return 0
	}
	for _, session := range result.Sessions {
		if *stuckOnly && !session.Status.Stuck() {
			continue
		}
		fmt.Println(rowText(session))
	}
	return 0
}

func rowText(session sessions.Session) string {
	mark := " "
	if session.Status.Stuck() {
		mark = "!"
	}
	reason := ""
	if session.Status.Label != "" {
		reason = "  [" + session.Status.Label + "]"
		if session.Status.CompactDetail() != "" {
			reason += " " + session.Status.CompactDetail()
		}
	}
	return fmt.Sprintf("%s %s  %s  %s%s\n    %s  %s",
		mark, session.Agent, session.ID, session.Title, reason,
		session.Updated, session.Cwd)
}

// ---- handoff -------------------------------------------------------------

func handoffCmd(args []string) int {
	flags := flag.NewFlagSet("handoff", flag.ContinueOnError)
	agent := flags.String("agent", "", "会话来源：opencode / qoder / qoder-cn / workbuddy / zcode")
	id := flags.String("id", "", "会话 ID")
	latest := flags.Bool("latest", false, "使用最近一个卡住的会话")
	note := flags.String("note", "", "给下一个 agent 的一句话")
	reason := flags.String("reason", "", "quota / upstream / aborted / switch / other")
	limit := flags.Int("limit", 40000, "对话块的字符上限，0 = 不限")
	asJSON := flags.Bool("json", false, "输出 JSON")
	full := flags.Bool("full", false, "带上压缩前的完整对话")
	noSummary := flags.Bool("no-summary", false, "不带压缩摘要")
	noMessages := flags.Bool("no-messages", false, "不带对话")
	noTodos := flags.Bool("no-todos", false, "不带待办")
	writeFile := flags.String("out", "", "把交接文本写入文件")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	detector := sessions.NewDetector()

	if *latest {
		result := detector.Refresh()
		var picked *sessions.Session
		for index := range result.Sessions {
			if result.Sessions[index].Status.Stuck() {
				picked = &result.Sessions[index]
				break
			}
		}
		if picked == nil && len(result.Sessions) > 0 {
			picked = &result.Sessions[0]
		}
		if picked == nil {
			fmt.Fprintln(os.Stderr, "baton: 没有找到任何会话")
			return 1
		}
		*agent, *id = picked.Agent, picked.ID
	}

	if *agent == "" || *id == "" {
		fmt.Fprintln(os.Stderr, "baton: 需要 --agent 和 --id（或用 --latest）")
		return 2
	}

	options := sessions.HandoffOptions{
		Note:     *note,
		Reason:   reasonCode(*reason),
		Summary:  !*noSummary,
		Messages: !*noMessages,
		Todos:    !*noTodos,
		Full:     *full,
		Limit:    *limit,
	}
	result, err := detector.Handoff(*agent, *id, options)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "baton: 会话不存在（可能已被删除）")
			return 1
		}
		fmt.Fprintln(os.Stderr, "baton: "+err.Error())
		return 1
	}

	if *writeFile != "" {
		path, err := filepath.Abs(*writeFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baton: "+err.Error())
			return 1
		}
		if err := os.WriteFile(path, []byte(result.Text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "baton: "+err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "已写入 %s（%d 字）\n", path, result.Chars)
		return 0
	}
	if *asJSON {
		encode(result)
		return 0
	}
	fmt.Println(result.Text)
	return 0
}

func reasonCode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "quota", "额度", "额度用完", "limit":
		return sessions.ReasonQuota
	case "upstream", "error", "报错", "上游报错":
		return sessions.ReasonUpstream
	case "aborted", "interrupt", "中断", "被中断":
		return sessions.ReasonAborted
	case "switch", "manual", "换人", "主动换人":
		return sessions.ReasonSwitch
	default:
		return sessions.ReasonOther
	}
}

func encode(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "baton: "+err.Error())
	}
}

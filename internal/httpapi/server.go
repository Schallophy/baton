// Package httpapi serves the embedded UI and the JSON endpoints behind it.
//
// Everything is loopback-only: the detectors read the user's own session stores
// and there is no authentication layer worth trusting on a public port.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Schallophy/baton/internal/sessions"
	"github.com/Schallophy/baton/web"
)

// Version is reported by /api/meta and the UI footer.
const Version = "0.1.0"

var staticPathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".json":  "application/json; charset=utf-8",
	".woff2": "font/woff2",
}

const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self' data:; base-uri 'none'; form-action 'none'"

// Server owns the router, the static assets and the detector.
type Server struct {
	Detector *sessions.Detector
	routes   map[string]handler
	version  string

	// Token guards POST /api/shutdown. Empty means the endpoint is not offered:
	// a server started without one should not accept anonymous exit requests.
	Token string
	// Stop is called when a valid shutdown request arrives.
	Stop func()
}

type handler func(*Ctx) error

// NewServer builds the HTTP surface around a detector. There are only four
// endpoints and none of them takes a path parameter, so the router is a plain
// method+path map: no pattern matching means no pattern bugs.
func NewServer(detector *sessions.Detector, version string) *Server {
	server := &Server{Detector: detector, version: version, routes: map[string]handler{}}
	server.add("GET", "/api/meta", server.handleMeta)
	server.add("GET", "/api/sessions", server.handleSessions)
	server.add("GET", "/api/session", server.handleSession)
	server.add("POST", "/api/handoff", server.handleHandoff)
	server.add("POST", "/api/shutdown", server.handleShutdown)
	return server
}

func (s *Server) add(method, pattern string, h handler) {
	s.routes[method+" "+strings.TrimSuffix(pattern, "/")] = h
}

func (s *Server) match(method, pathname string) handler {
	return s.routes[method+" "+strings.TrimSuffix(pathname, "/")]
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("x-content-type-options", "nosniff")
	header.Set("referrer-policy", "no-referrer")
	header.Set("content-security-policy", contentSecurityPolicy)
	header.Set("x-frame-options", "SAMEORIGIN")

	if !isLoopback(r.RemoteAddr) {
		writeError(w, 403, "界面仅允许本机访问")
		return
	}

	ctx := &Ctx{W: w, R: r, URL: r.URL}
	defer func() {
		if recovered := recover(); recovered != nil {
			writeError(w, 500, fmt.Sprint(recovered))
		}
	}()

	if r.Method == "POST" {
		if err := ctx.readBody(); err != nil {
			writeError(w, err.Status, err.Message)
			return
		}
	}

	if matched := s.match(r.Method, r.URL.Path); matched != nil {
		if err := matched(ctx); err != nil {
			status := 500
			message := err.Error()
			var httpErr *HttpError
			if errors.As(err, &httpErr) {
				status, message = httpErr.Status, httpErr.Message
			}
			writeError(w, status, message)
		}
		return
	}

	if r.Method == "GET" || r.Method == "HEAD" {
		if s.serveStatic(ctx) {
			return
		}
	}
	writeError(w, 404, "未找到 "+r.URL.Path)
}

func (s *Server) serveStatic(ctx *Ctx) bool {
	pathname := ctx.URL.Path
	relative := "index.html"
	if pathname != "/" {
		relative = strings.TrimLeft(pathname, "/")
	}
	if !staticPathPattern.MatchString(relative) || strings.Contains(relative, "..") {
		return false
	}
	content, err := fs.ReadFile(web.FS, relative)
	if err != nil {
		return false
	}
	contentType := mimeTypes[strings.ToLower(path.Ext(relative))]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.W.Header().Set("content-type", contentType)
	ctx.W.Header().Set("cache-control", "no-store")
	ctx.W.WriteHeader(200)
	if ctx.R.Method != "HEAD" {
		_, _ = ctx.W.Write(content)
	}
	return true
}

// Ctx is the request context handed to handlers.
type Ctx struct {
	W   http.ResponseWriter
	R   *http.Request
	URL *url.URL
	// Body holds the parsed JSON request body for POST handlers.
	Body map[string]any
}

// HttpError aborts a handler with a status code.
type HttpError struct {
	Status  int
	Message string
}

func (e *HttpError) Error() string { return e.Message }

func (c *Ctx) readBody() *HttpError {
	data, err := io.ReadAll(io.LimitReader(c.R.Body, 1<<20))
	if err != nil {
		return &HttpError{Status: 400, Message: "读取请求失败：" + err.Error()}
	}
	// PowerShell 的 Out-File / Set-Content 会带 UTF-8 BOM；脚本用 curl 发请求时
	// 很容易踩到，剥掉比报「不是合法 JSON」更有用。
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		c.Body = map[string]any{}
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return &HttpError{Status: 400, Message: "请求体不是合法 JSON"}
	}
	c.Body = body
	return nil
}

func (c *Ctx) send(status int, data any) {
	c.W.Header().Set("content-type", "application/json; charset=utf-8")
	c.W.Header().Set("cache-control", "no-store")
	c.W.WriteHeader(status)
	encoder := json.NewEncoder(c.W)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	payload := map[string]any{"error": map[string]any{"message": message}}
	data, _ := json.Marshal(payload)
	_, _ = w.Write(data)
}

func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.TrimPrefix(strings.TrimSpace(host), "::ffff:")
	if host == "" {
		return false
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.IsLoopback()
	}
	return strings.HasPrefix(host, "127.")
}

package httpapi

import (
	"errors"
	"os"
	"strings"

	"github.com/Schallophy/baton/internal/sessions"
)

func (s *Server) handleMeta(ctx *Ctx) error {
	ctx.send(200, map[string]any{
		"version":  s.version,
		"home":     s.Detector.Home(),
		"reasons":  reasonOptions(),
		"defaults": map[string]any{"limit": 40000},
	})
	return nil
}

// reasonOptions is what the UI's reason picker offers. "detected" is not a
// choice: it means "keep whatever the store says".
func reasonOptions() []map[string]string {
	return []map[string]string{
		{"code": sessions.ReasonQuota, "label": "额度用完"},
		{"code": sessions.ReasonUpstream, "label": "上游报错"},
		{"code": sessions.ReasonAborted, "label": "被中断"},
		{"code": sessions.ReasonSwitch, "label": "主动换人"},
		{"code": sessions.ReasonOther, "label": "其他原因"},
	}
}

func (s *Server) handleSessions(ctx *Ctx) error {
	if ctx.URL.Query().Get("refresh") == "1" {
		ctx.send(200, s.Detector.Refresh())
		return nil
	}
	ctx.send(200, s.Detector.List())
	return nil
}

func (s *Server) handleSession(ctx *Ctx) error {
	agent := strings.TrimSpace(ctx.URL.Query().Get("agent"))
	id := strings.TrimSpace(ctx.URL.Query().Get("id"))
	if agent == "" || id == "" {
		return &HttpError{Status: 400, Message: "缺少 agent 或 id"}
	}
	result, err := s.Detector.Detail(agent, id)
	if err != nil {
		return sessionError(err)
	}
	ctx.send(200, result)
	return nil
}

func (s *Server) handleHandoff(ctx *Ctx) error {
	agent := strings.TrimSpace(stringValue(ctx.Body, "agent"))
	id := strings.TrimSpace(stringValue(ctx.Body, "id"))
	if agent == "" || id == "" {
		return &HttpError{Status: 400, Message: "缺少 agent 或 id"}
	}

	options := sessions.HandoffOptions{
		Note:     stringValue(ctx.Body, "note"),
		Reason:   reasonCode(stringValue(ctx.Body, "reason")),
		Summary:  boolValue(ctx.Body, "summary", true),
		Messages: boolValue(ctx.Body, "messages", true),
		Todos:    boolValue(ctx.Body, "todos", true),
		Full:     boolValue(ctx.Body, "full", false),
		Limit:    intValue(ctx.Body, "limit", 40000),
	}
	result, err := s.Detector.Handoff(agent, id, options)
	if err != nil {
		return sessionError(err)
	}
	ctx.send(200, result)
	return nil
}

// handleShutdown lets `baton stop` end the background instance. The token comes
// from the state file, so only a local process that could read it can call this;
// a page in a browser cannot set the header cross-origin. The reply is sent
// before the server stops, otherwise the CLI would read a connection reset.
func (s *Server) handleShutdown(ctx *Ctx) error {
	if s.Token == "" || s.Stop == nil {
		return &HttpError{Status: 404, Message: "该实例不支持远程退出"}
	}
	if !strings.EqualFold(ctx.R.Header.Get("x-baton-token"), s.Token) {
		return &HttpError{Status: 403, Message: "令牌不匹配"}
	}
	ctx.send(200, map[string]any{"stopping": true})
	if s.Stop != nil {
		// 让响应先写完，再真正停下。
		go s.Stop()
	}
	return nil
}

func sessionError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &HttpError{Status: 404, Message: "会话不存在或已被删除"}
	}
	return &HttpError{Status: 502, Message: err.Error()}
}

// reasonCode normalises a reason code coming from the UI or a client. An empty
// value means "use the detected one".
func reasonCode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "quota":
		return sessions.ReasonQuota
	case "upstream":
		return sessions.ReasonUpstream
	case "aborted":
		return sessions.ReasonAborted
	case "switch":
		return sessions.ReasonSwitch
	case "other":
		return sessions.ReasonOther
	}
	return sessions.ReasonDetected
}

func stringValue(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func boolValue(body map[string]any, key string, fallback bool) bool {
	value, ok := body[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func intValue(body map[string]any, key string, fallback int) int {
	value, ok := body[key].(float64)
	if !ok {
		return fallback
	}
	return int(value)
}

package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"qanat/internal/codex"
	"qanat/internal/core/oauth"
	"qanat/internal/protocol"
	"qanat/internal/proxy"
)

// sendCodex is send for Codex accounts (§7): the Messages body becomes a
// Responses call on the ChatGPT Codex backend, and the answer comes back as
// an Anthropic response, so forward and stream treat it like Claude's: 401
// refreshes the sign-in, 429 cools the account down until the reset Codex
// names, usage is read from the translated events.
func (g *Gateway) sendCodex(r *http.Request, a *Account, sp *proxy.Spec, body []byte) (*http.Response, error) {
	model, _, _, err := protocol.ModelName(body)
	if err != nil {
		return anthropicResponse(http.StatusBadRequest, errorJSON("invalid_request_error", "Invalid request body: "+err.Error())), nil
	}
	if strings.HasSuffix(r.URL.Path, "count_tokens") {
		// The backend has no token counter; Claude Code only sizes its
		// context with this, so ~4 bytes a token will do.
		return anthropicResponse(http.StatusOK, fmt.Appendf(nil, `{"input_tokens":%d}`, len(body)/4)), nil
	}
	if body, err = g.inlineURLMedia(r.Context(), sp, body); err != nil {
		return anthropicResponse(http.StatusBadRequest, errorJSON("invalid_request_error", err.Error())), nil
	}
	session := r.Header.Get("X-Claude-Code-Session-Id")
	if session == "" {
		session = a.ID
	}
	tr, err := codex.Translate(body, codex.Options{Model: model, Session: session})
	if err != nil {
		return anthropicResponse(http.StatusBadRequest, errorJSON("invalid_request_error", "Invalid request body: "+err.Error())), nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimSuffix(a.baseURL(), "/")+"/responses", bytes.NewReader(tr.Body))
	if err != nil {
		return nil, err
	}
	oauth.SetCodexHeaders(req.Header, a.Token, a.ChatGPTAccount)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("session_id", session)
	resp, err := g.proxies.Client(sp).Do(withConnectTrace(req, sp))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		g.log.Warn("codex refused the request", "account", a.ID, "model", model, "status", resp.StatusCode, "body", string(raw[:min(len(raw), 500)]))
		out := anthropicResponse(resp.StatusCode, codex.ErrorBody(resp.StatusCode, raw))
		if resp.StatusCode == http.StatusTooManyRequests {
			if d := codex.RetryAfter(raw, time.Now()); d > 0 {
				out.Header.Set("Retry-After", strconv.Itoa(int(d.Round(time.Second)/time.Second)))
			}
		}
		return out, nil
	}
	st := codex.NewStream(resp.Body, model, tr.Tools)
	if tr.Stream {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: st}, nil
	}
	msg, err := protocol.Collect(st)
	st.Close()
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err) // nothing sent yet: the next account may answer
	}
	return anthropicResponse(http.StatusOK, msg), nil
}

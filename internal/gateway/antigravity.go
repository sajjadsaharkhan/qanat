package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"qanat/internal/antigravity"
	"qanat/internal/core/oauth"
	"qanat/internal/protocol"
	"qanat/internal/proxy"
)

// maxAntigravityCooldown bounds a 429's Retry-After: Google may name a quota
// reset hours away for one model while the account's other models still work.
const maxAntigravityCooldown = 10 * time.Minute

// sendAntigravity is send for Antigravity accounts (§7): the Messages body
// becomes a Cloud Code streamGenerateContent call, and the answer comes back
// as an Anthropic response, so forward and stream treat it like Claude's:
// 401 refreshes the sign-in, 429 cools the account down, usage is read from
// the translated events.
func (g *Gateway) sendAntigravity(r *http.Request, a *Account, sp *proxy.Spec, body []byte) (*http.Response, error) {
	model, _, _, err := protocol.ModelName(body)
	if err != nil {
		return anthropicResponse(http.StatusBadRequest, errorJSON("invalid_request_error", "Invalid request body: "+err.Error())), nil
	}
	if strings.HasSuffix(r.URL.Path, "count_tokens") {
		// Cloud Code has no token counter for the IDE's models; Claude Code
		// only sizes its context with this, so ~4 bytes a token will do.
		return anthropicResponse(http.StatusOK, fmt.Appendf(nil, `{"input_tokens":%d}`, len(body)/4)), nil
	}
	session := r.Header.Get("X-Claude-Code-Session-Id")
	tr, err := antigravity.Translate(body, antigravity.Options{Model: model, Project: a.Project, Session: a.ID + ":" + session})
	if err != nil {
		return anthropicResponse(http.StatusBadRequest, errorJSON("invalid_request_error", "Invalid request body: "+err.Error())), nil
	}
	url := strings.TrimSuffix(a.baseURL(), "/") + "/v1internal:streamGenerateContent?alt=sse"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(tr.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("User-Agent", oauth.AntigravityUserAgent)
	resp, err := g.proxies.Client(sp).Do(withConnectTrace(req, sp))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		out := anthropicResponse(resp.StatusCode, antigravity.ErrorBody(resp.StatusCode, raw))
		if resp.StatusCode == http.StatusTooManyRequests {
			if d := min(antigravity.RetryAfter(raw, time.Now()), maxAntigravityCooldown); d > 0 {
				out.Header.Set("Retry-After", strconv.Itoa(int(d.Round(time.Second)/time.Second)))
			}
		}
		return out, nil
	}
	st := antigravity.NewStream(resp.Body, model, tr.Tools)
	if tr.Stream {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: st}, nil
	}
	msg, err := antigravity.Collect(st)
	st.Close()
	if err != nil {
		return nil, fmt.Errorf("antigravity: %w", err) // nothing sent yet: the next account may answer
	}
	return anthropicResponse(http.StatusOK, msg), nil
}

func anthropicResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func errorJSON(typ, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
	return b
}

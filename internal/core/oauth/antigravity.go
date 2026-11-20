package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Antigravity signs in with Google as the Antigravity IDE does (9router
// registry/antigravity.js and lib/oauth/providers/antigravity.js, MIT): the
// IDE's installed-app client, offline access, consent forced so Google always
// returns a refresh token. Requests to Cloud Code carry the IDE's UA.

// AntigravityUserAgent is the Antigravity IDE's UA (9router's
// ANTIGRAVITY_IDE_USER_AGENT).
const AntigravityUserAgent = "antigravity/hub/2.24.0 darwin/arm64"

// Cloud Code endpoints. Vars so tests can point them at a fake.
var (
	// CloudCodeURL serves loadCodeAssist and onboardUser.
	CloudCodeURL = "https://cloudcode-pa.googleapis.com"
	// AntigravityAPIURL is the IDE's model API (fetchAvailableModels).
	AntigravityAPIURL = "https://daily-cloudcode-pa.googleapis.com"
)

// onboardWait is the pause between onboardUser polls (9router waits 5 s).
var onboardWait = 5 * time.Second

var antigravity = &Flow{
	ID:           "antigravity",
	AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:     "https://oauth2.googleapis.com/token",
	ClientID:     "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
	// An installed-app client: Google's docs treat this secret as public.
	ClientSecret: "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",
	Scope: strings.Join([]string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	}, " "),
	CallbackPath:    "/callback",
	AuthorizeExtra:  url.Values{"access_type": {"offline"}, "prompt": {"consent"}},
	ProfileURL:      "https://www.googleapis.com/oauth2/v1/userinfo",
	tokensEncoding:  "form",
	refreshEncoding: "application/x-www-form-urlencoded",
	refreshBody: func(f *Flow, refreshToken string) ([]byte, error) {
		return []byte(url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
			"client_id": {f.ClientID}, "client_secret": {f.ClientSecret},
		}.Encode()), nil
	},
}

// clientMetadata is the IDE's ClientMetadata with numeric enums: ideType 9
// is Antigravity, pluginType 2 is Gemini; platform follows this host.
func clientMetadata() map[string]int {
	platform := 0
	switch runtime.GOOS {
	case "darwin":
		platform = 1
		if runtime.GOARCH == "arm64" {
			platform = 2
		}
	case "linux":
		platform = 3
		if runtime.GOARCH == "arm64" {
			platform = 4
		}
	case "windows":
		platform = 5
	}
	return map[string]int{"ideType": 9, "platform": platform, "pluginType": 2}
}

// antigravitySetup runs after the exchange: the account email from Google's
// userinfo, then the Cloud Code project and tier from loadCodeAssist, then
// onboarding. A project already assigned onboards in the background like
// 9router; a first-time account has none until onboarding finishes, so that
// one is waited for (bounded) to learn its project.
func antigravitySetup(ctx context.Context, hc *http.Client, t *Tokens) {
	t.Email = googleEmail(ctx, hc, antigravity.ProfileURL, t.AccessToken)

	project, tier, err := LoadCodeAssist(ctx, hc, t.AccessToken)
	if err != nil {
		return // the account still signs in; Test shows the Cloud Code error
	}
	base, wait, token := CloudCodeURL, onboardWait, t.AccessToken
	if project != "" {
		t.ProjectID = project
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			onboard(bg, hc, base, token, tier, 10, wait)
		}()
		return
	}
	t.ProjectID = onboard(ctx, hc, base, token, tier, 4, wait)
}

// googleEmail reads the signed-in account's email; "" when unavailable.
func googleEmail(ctx context.Context, hc *http.Client, userInfoURL, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL+"?alt=json", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-request-source", "local")
	var u struct {
		Email string `json:"email"`
	}
	if getJSON(hc, req, &u) != nil {
		return ""
	}
	return u.Email
}

// LoadCodeAssistRequest is the loadCodeAssist call the IDE makes on start.
// The account Test button sends it too: it needs a valid token and costs no
// quota.
func LoadCodeAssistRequest(ctx context.Context, token string) (*http.Request, error) {
	return cloudCodeRequest(ctx, CloudCodeURL+"/v1internal:loadCodeAssist", token,
		map[string]any{"metadata": clientMetadata()})
}

// LoadCodeAssist returns the account's Cloud Code project ("" before
// onboarding) and its default tier.
func LoadCodeAssist(ctx context.Context, hc *http.Client, token string) (project, tier string, err error) {
	req, err := LoadCodeAssistRequest(ctx, token)
	if err != nil {
		return "", "", err
	}
	var wire struct {
		Project      json.RawMessage `json:"cloudaicompanionProject"`
		AllowedTiers []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if err := getJSON(hc, req, &wire); err != nil {
		return "", "", err
	}
	tier = "legacy-tier"
	for _, t := range wire.AllowedTiers {
		if t.IsDefault && t.ID != "" {
			tier = strings.TrimSpace(t.ID)
			break
		}
	}
	return projectID(wire.Project), tier, nil
}

// onboard polls onboardUser until the operation is done, at most attempts
// times, and returns the project it reports ("" if none).
func onboard(ctx context.Context, hc *http.Client, base, token, tier string, attempts int, wait time.Duration) string {
	for i := 0; i < attempts; i++ {
		req, err := cloudCodeRequest(ctx, base+"/v1internal:onboardUser", token,
			map[string]any{"tierId": tier, "metadata": clientMetadata()})
		if err != nil {
			return ""
		}
		var op struct {
			Done     bool `json:"done"`
			Response struct {
				Project json.RawMessage `json:"cloudaicompanionProject"`
			} `json:"response"`
		}
		if getJSON(hc, req, &op) != nil {
			return ""
		}
		if op.Done {
			return projectID(op.Response.Project)
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(wait):
		}
	}
	return ""
}

// AvailableModelsRequest asks the IDE's model API which models the account
// may use (the Models page's catalog sync).
func AvailableModelsRequest(ctx context.Context, token, project string) (*http.Request, error) {
	body := map[string]any{}
	if project != "" {
		body["project"] = project
	}
	req, err := cloudCodeRequest(ctx, AntigravityAPIURL+"/v1internal:fetchAvailableModels", token, body)
	if err == nil {
		req.Header.Set("X-Client-Name", "antigravity")
		req.Header.Set("X-Client-Version", "2.24.0")
	}
	return req, err
}

// ParseAvailableModels reads model IDs from fetchAvailableModels: an object
// keyed by model ID (internal ones skipped), or a list in older answers.
func ParseAvailableModels(body []byte) ([]string, error) {
	var wire struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	var byID map[string]struct {
		IsInternal bool `json:"isInternal"`
	}
	if json.Unmarshal(wire.Models, &byID) == nil {
		out := make([]string, 0, len(byID))
		for id, m := range byID {
			if !m.IsInternal {
				out = append(out, id)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	var list []struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(wire.Models, &list); err != nil {
		return nil, fmt.Errorf("unexpected models shape")
	}
	var out []string
	for _, m := range list {
		switch {
		case m.ID != "":
			out = append(out, m.ID)
		case m.Model != "":
			out = append(out, m.Model)
		case m.Name != "":
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	return out, nil
}

func cloudCodeRequest(ctx context.Context, endpoint, token string, body any) (*http.Request, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", AntigravityUserAgent)
	req.Header.Set("x-request-source", "local")
	return req, nil
}

func getJSON(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s answered %d", req.URL.Path, resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// projectID accepts cloudaicompanionProject as a string or as {"id": …}.
func projectID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var o struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return strings.TrimSpace(o.ID)
	}
	return ""
}

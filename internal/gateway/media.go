package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"qanat/internal/proxy"
)

// URL media (images and PDFs given by address) is fetched by the gateway for
// the translated providers: Codex and Antigravity can't fetch it themselves.
// The client chooses the address, so the fetch is fenced: http(s) only, no
// private, loopback or link-local destinations (redirects included), a size
// cap and a timeout.
const maxMediaBytes = 10 << 20

var (
	mediaTimeout = 15 * time.Second
	// mediaIPAllowed decides which addresses may be fetched. A var so tests
	// can reach httptest servers on loopback.
	mediaIPAllowed = publicIP

	errPrivateHost = errors.New("the address points to a private or local network")
)

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsInterfaceLocalMulticast() && !ip.IsMulticast() &&
		!ip.IsUnspecified() && !cgnat.Contains(ip)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// directMediaClient checks the address it actually connects to, so DNS
// can't swap in a private one after the check.
var directMediaClient = &http.Client{Transport: &http.Transport{
	Proxy: nil,
	DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		if !mediaIPAllowed(net.ParseIP(host)) {
			return errPrivateHost
		}
		return nil
	}}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second,
}}

// inlineURLMedia replaces image and document blocks with a URL source,
// anywhere in the messages (tool results too), by base64 sources. A body
// with no URL source comes back unchanged and unparsed.
func (g *Gateway) inlineURLMedia(ctx context.Context, sp *proxy.Spec, body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"url"`)) {
		return body, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return body, nil // the translator reports a bad body
	}
	changed := false
	var walk func(v any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				if err := walk(e); err != nil {
					return err
				}
			}
		case map[string]any:
			src, _ := x["source"].(map[string]any)
			kind, _ := x["type"].(string)
			if u, _ := src["url"].(string); src["type"] == "url" && u != "" && (kind == "image" || kind == "document") {
				mt, data, err := g.fetchMedia(ctx, sp, u, kind)
				if err != nil {
					return fmt.Errorf("Qanat couldn't fetch the %s at %s: %w", kind, u, err)
				}
				x["source"] = map[string]any{"type": "base64", "media_type": mt, "data": data}
				changed = true
				return nil
			}
			if c, ok := x["content"]; ok {
				return walk(c)
			}
		}
		return nil
	}
	if err := walk(doc["messages"]); err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(doc)
}

func (g *Gateway) fetchMedia(ctx context.Context, sp *proxy.Spec, raw, kind string) (mediaType, data string, err error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", errors.New("only http and https addresses can be fetched")
	}
	client := directMediaClient
	if sp != nil {
		// Through the account's proxy the dial goes to the proxy, so the
		// destination is checked by name here and on every redirect.
		c := *g.proxies.Client(sp)
		c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return checkHost(r.Context(), r.URL.Hostname())
		}
		client = &c
		if err := checkHost(ctx, u.Hostname()); err != nil {
			return "", "", err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, errPrivateHost) {
			return "", "", errPrivateHost
		}
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("the server answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(b) > maxMediaBytes {
		return "", "", fmt.Errorf("it is larger than %d MB", maxMediaBytes>>20)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt == "" || mt == "application/octet-stream" {
		mt, _, _ = mime.ParseMediaType(http.DetectContentType(b))
	}
	switch {
	case kind == "image" && strings.HasPrefix(mt, "image/"):
	case kind == "document" && mt == "application/pdf":
	default:
		return "", "", fmt.Errorf("it is %s, not %s", mt, map[string]string{"image": "an image", "document": "a PDF"}[kind])
	}
	return mt, base64.StdEncoding.EncodeToString(b), nil
}

// checkHost refuses a name that resolves to any non-public address.
func checkHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if !mediaIPAllowed(ip) {
			return errPrivateHost
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if !mediaIPAllowed(a.IP) {
			return errPrivateHost
		}
	}
	return nil
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/thanhenti/bepilot/internal/skills"
)

// --- current_time ---------------------------------------------------------—-

type currentTimeArgs struct {
	Timezone string `json:"timezone" jsonschema_description:"IANA timezone name, e.g. 'Asia/Ho_Chi_Minh'. Defaults to UTC."`
}

func newCurrentTimeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"current_time",
		"Return the current date and time. Use whenever the answer depends on 'now' (deadlines, ages, schedules, recency).",
		func(_ context.Context, a currentTimeArgs) (map[string]any, error) {
			loc := time.UTC
			if a.Timezone != "" {
				l, err := time.LoadLocation(a.Timezone)
				if err != nil {
					return nil, fmt.Errorf("unknown timezone %q", a.Timezone)
				}
				loc = l
			}
			now := time.Now().In(loc)
			return map[string]any{
				"iso8601":  now.Format(time.RFC3339),
				"date":     now.Format("2006-01-02"),
				"time":     now.Format("15:04:05"),
				"weekday":  now.Weekday().String(),
				"timezone": loc.String(),
				"unix":     now.Unix(),
			}, nil
		},
	)
}

// --- http_fetch ----------------------------------------------------------—-—

type httpFetchArgs struct {
	URL string `json:"url" jsonschema:"required" jsonschema_description:"Absolute http(s) URL to GET."`
}

func newHTTPFetchTool(allowlist []string) (tool.InvokableTool, error) {
	allow := map[string]bool{}
	for _, h := range allowlist {
		allow[strings.ToLower(strings.TrimSpace(h))] = true
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	return utils.InferTool(
		"http_fetch",
		"Fetch a web page or API endpoint over HTTP GET and return its text body (truncated). Use for reading a known URL; not a search engine.",
		func(ctx context.Context, a httpFetchArgs) (map[string]any, error) {
			u := strings.TrimSpace(a.URL)
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return nil, fmt.Errorf("url must be absolute http(s)")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return nil, err
			}
			host := strings.ToLower(req.URL.Hostname())
			if len(allow) > 0 && !allow[host] {
				return nil, fmt.Errorf("host %q is not in the http_fetch allowlist", host)
			}
			if isPrivateHost(host) {
				return nil, fmt.Errorf("refusing to fetch private/loopback host %q", host)
			}
			req.Header.Set("User-Agent", "bepilot/1.0 (+http_fetch)")

			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 200_000))
			return map[string]any{
				"status":       resp.StatusCode,
				"content_type": resp.Header.Get("Content-Type"),
				"url":          resp.Request.URL.String(),
				"body":         string(body),
				"truncated":    len(body) == 200_000,
			}, nil
		},
	)
}

// maxFetchBytes caps how much of a response http_fetch returns.
const maxFetchBytes = 200_000

// bodyText turns a response body into valid UTF-8. The byte cap can cut a
// multi-byte character in half, and pages in a legacy code page (Windows-125x)
// contain bytes that are not UTF-8 at all; passing either on unchanged makes
// the tool result impossible to store or to send to the model as JSON.
func bodyText(b []byte, truncated bool) string {
	if truncated {
		// Drop a character cut by the cap rather than turning it into U+FFFD.
		for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
			if utf8.RuneStart(b[len(b)-i]) {
				if !utf8.FullRune(b[len(b)-i:]) {
					b = b[:len(b)-i]
				}
				break
			}
		}
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

func isPrivateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// Can't resolve: let the HTTP client fail naturally rather than block.
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

// --- load_skill --------------------------------------------------------—-—-—

type loadSkillArgs struct {
	Slug string `json:"slug" jsonschema:"required" jsonschema_description:"The skill slug, exactly as listed in the system prompt's available skills."`
}

func newLoadSkillTool(svc *skills.Service) (tool.InvokableTool, error) {
	return utils.InferTool(
		"load_skill",
		"Load the full instructions for one of the skills listed in the system prompt. Call this BEFORE attempting a task a listed skill covers; it returns step-by-step guidance and the names of bundled resource files.",
		func(ctx context.Context, a loadSkillArgs) (skills.Loaded, error) {
			if svc == nil {
				return skills.Loaded{}, fmt.Errorf("skills are not available")
			}
			loaded, err := svc.Load(ctx, strings.TrimSpace(a.Slug))
			if err != nil {
				return skills.Loaded{}, fmt.Errorf("load_skill %q: %w", a.Slug, err)
			}
			return loaded, nil
		},
	)
}

// --- web_search (stub) ------------------------------------------------—-—-—-

type webSearchArgs struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"Search query."`
}

// newWebSearchTool returns a web_search tool. Without a configured search
// backend it returns a clear, non-fatal message so the model can fall back to
// its own knowledge or http_fetch instead of hallucinating results.
func newWebSearchTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"web_search",
		"Search the web for up-to-date information. Use for recent events or facts you are unsure about.",
		func(_ context.Context, a webSearchArgs) (map[string]any, error) {
			return map[string]any{
				"query":   a.Query,
				"results": []any{},
				"note":    "web_search backend is not configured on this deployment; answer from prior knowledge or use http_fetch with a specific URL.",
			}, nil
		},
	)
}

var _ = json.Marshal

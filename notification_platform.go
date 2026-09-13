// notification_platform.go defines the outbound delivery seam shared by the
// three group-bot platform adapters (WeCom, Feishu, DingTalk): the message
// and result types, the adapter factory and the message splitter that keeps
// every part inside the platform byte budget.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Platform byte budgets (official limits, spec matrix row "字节限制和拆分"):
// oversized bodies are split on section boundaries before sending.
const (
	wecomMarkdownLimit    = 4096
	dingtalkMarkdownLimit = 20000
	feishuTextLimit       = 30 << 10
)

// outboundMessage is one rendered notification body ready for delivery.
type outboundMessage struct {
	Title   string
	Body    string
	UserIDs []string
}

// deliveryResult is the per-part delivery outcome. Detail stays closed: it
// must never contain the webhook URL or the raw platform response body.
type deliveryResult struct {
	Outcome    string
	ErrorCode  string
	Detail     string
	RetryAfter time.Duration
}

// platformAdapter sends one message to its platform. The returned slice
// carries one result per split part, in order.
type platformAdapter interface {
	send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error)
}

// buildAdapter returns the adapter for one platform identity. All adapters
// share a 10s HTTP timeout; unknown kinds are a closed configuration error.
func buildAdapter(p PlatformIdentity) (platformAdapter, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	switch p.Kind {
	case PlatformWeCom:
		return &wecomAdapter{client: client, webhook: p.Webhook, userIDs: nonEmptyIDs(p.UserIDs)}, nil
	case PlatformFeishu:
		return &feishuAdapter{client: client, webhook: p.Webhook, secret: p.SignSecret, userIDs: nonEmptyIDs(p.UserIDs)}, nil
	case PlatformDingTalk:
		return &dingtalkAdapter{client: client, webhook: p.Webhook, secret: p.SignSecret, userIDs: nonEmptyIDs(p.UserIDs)}, nil
	default:
		return nil, &keeperError{Code: "configuration_error"}
	}
}

// nonEmptyIDs drops blank entries so platform @-lists never carry padding.
func nonEmptyIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}
	return out
}

// splitMessage packs "\n\n"-separated sections greedily into parts of at most
// limit bytes; a single oversized section is hard-cut.
func splitMessage(body string, limit int) []string {
	if len(body) <= limit {
		return []string{body}
	}
	var parts []string
	current := ""
	for _, section := range strings.Split(body, "\n\n") {
		for len(section) > limit { // 单节超限硬切
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
			parts = append(parts, section[:limit])
			section = section[limit:]
		}
		candidate := section
		if current != "" {
			candidate = current + "\n\n" + section
		}
		if len(candidate) > limit {
			parts = append(parts, current)
			current = section
		} else {
			current = candidate
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// postJSON issues one platform POST with a JSON body and returns the HTTP
// status, the raw response body and the parsed Retry-After hint. Transport
// failures surface as errors so callers can report a deliveryUnknown outcome
// (the HTTP result was never observed). Callers map every failure to closed
// delivery codes: neither the webhook URL nor the response body text may
// leak into deliveryResult.Detail.
func postJSON(ctx context.Context, client *http.Client, url string, body any) (statusCode int, respBody []byte, retryAfter time.Duration, err error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, 0, err
	}
	return resp.StatusCode, respBody, parseRetryAfter(resp.Header.Get("Retry-After")), nil
}

// parseRetryAfter reads the seconds form of the Retry-After header; other
// forms (or garbage) yield zero.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// transportUnknownResult reports that the delivery outcome could not be
// observed (timeout, connection failure) — the platform must not be assumed
// to have failed or succeeded.
func transportUnknownResult() deliveryResult {
	return deliveryResult{Outcome: deliveryUnknown, ErrorCode: "transport_unknown"}
}

// rateLimitedResult reports an HTTP 429 rejection carrying the platform's
// Retry-After hint.
func rateLimitedResult(retryAfter time.Duration) deliveryResult {
	return deliveryResult{Outcome: deliveryFailed, ErrorCode: "rate_limited", RetryAfter: retryAfter}
}

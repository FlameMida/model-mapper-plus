// notification_platform_dingtalk.go delivers markdown messages to DingTalk
// group bots. When a signing secret is configured the webhook URL gains
// timestamp + sign query parameters (HMAC-SHA256 over "<ms>\n<secret>",
// base64 then query-escaped). @-mentions ride the at.atUserIds field. HTTP
// 200 with errcode != 0 is a delivery failure, never a forged success.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type dingtalkAdapter struct {
	client  *http.Client
	webhook string
	secret  string
	userIDs []string
}

func (a *dingtalkAdapter) send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error) {
	parts := splitMessage(msg.Body, dingtalkMarkdownLimit)
	results := make([]deliveryResult, 0, len(parts))
	for _, part := range parts {
		payload := map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": msg.Title, "text": part},
			"at":       map[string]any{"atUserIds": a.userIDs},
		}
		status, respBody, retryAfter, err := postJSON(ctx, a.client, a.signedURL(), payload)
		switch {
		case err != nil:
			results = append(results, transportUnknownResult())
		case status == http.StatusTooManyRequests:
			results = append(results, rateLimitedResult(retryAfter))
		default:
			var env struct {
				Errcode int `json:"errcode"`
			}
			_ = json.Unmarshal(respBody, &env)
			if env.Errcode != 0 {
				results = append(results, deliveryResult{
					Outcome:   deliveryFailed,
					ErrorCode: "dingtalk_errcode",
					Detail:    fmt.Sprintf("errcode=%d", env.Errcode),
				})
			} else {
				results = append(results, deliveryResult{Outcome: deliveryAccepted})
			}
		}
	}
	return results, nil
}

// signedURL appends the DingTalk timestamp+sign query parameters when a
// secret is configured; without a secret the webhook URL is used as-is.
func (a *dingtalkAdapter) signedURL() string {
	if a.secret == "" {
		return a.webhook
	}
	ms := time.Now().UnixMilli()
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write([]byte(strconv.FormatInt(ms, 10) + "\n" + a.secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	sep := "&"
	if !strings.Contains(a.webhook, "?") {
		sep = "?"
	}
	return a.webhook + sep + "timestamp=" + strconv.FormatInt(ms, 10) + "&sign=" + url.QueryEscape(sign)
}

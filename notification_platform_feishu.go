// notification_platform_feishu.go delivers text messages to Feishu group
// bots. When a signing secret is configured the request body carries
// timestamp + sign (HMAC-SHA256 over "<ts>\n<secret>", base64). @-mentions
// are inline text markers and only Open IDs (ou_ prefix) resolve in external
// groups, so other IDs are appended as a plain-text list. HTTP 200 with
// code != 0 is a delivery failure, never a forged success.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type feishuAdapter struct {
	client  *http.Client
	webhook string
	secret  string
	userIDs []string
}

func (a *feishuAdapter) send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error) {
	parts := splitMessage(msg.Body, feishuTextLimit)
	results := make([]deliveryResult, 0, len(parts))
	for _, part := range parts {
		payload := map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": feishuComposeText(a.userIDs, part)},
		}
		if a.secret != "" {
			ts := time.Now().Unix()
			payload["timestamp"] = strconv.FormatInt(ts, 10)
			payload["sign"] = feishuSign(a.secret, ts)
		}
		status, respBody, retryAfter, err := postJSON(ctx, a.client, a.webhook, payload)
		switch {
		case err != nil:
			results = append(results, transportUnknownResult())
		case status == http.StatusTooManyRequests:
			results = append(results, rateLimitedResult(retryAfter))
		default:
			var env struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(respBody, &env)
			if env.Code != 0 {
				results = append(results, deliveryResult{
					Outcome:   deliveryFailed,
					ErrorCode: "feishu_code",
					Detail:    fmt.Sprintf("code=%d", env.Code),
				})
			} else {
				results = append(results, deliveryResult{Outcome: deliveryAccepted})
			}
		}
	}
	return results, nil
}

// feishuComposeText prefixes inline @-markers for Open IDs and appends any
// remaining (non Open-ID) user references as a plain-text list at the end of
// the text: external Feishu groups only resolve Open IDs (spec).
func feishuComposeText(userIDs []string, body string) string {
	var inline strings.Builder
	var others []string
	for _, id := range userIDs {
		if strings.HasPrefix(id, "ou_") {
			inline.WriteString("<at user_id=\"" + id + "\"></at> ")
		} else {
			others = append(others, id)
		}
	}
	text := inline.String() + body
	if len(others) > 0 {
		text += "\n@" + strings.Join(others, " @")
	}
	return text
}

// feishuSign computes the Feishu custom-bot signature: base64 of
// HMAC-SHA256 keyed by the secret over "<unix-seconds>\n<secret>".
func feishuSign(secret string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

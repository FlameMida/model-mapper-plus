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
	atAll   bool
}

func (a *feishuAdapter) send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error) {
	parts := splitMessage(msg.Body, feishuTextLimit)
	results := make([]deliveryResult, 0, len(parts))
	for _, part := range parts {
		payload := map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": feishuComposeText(a.userIDs, a.atAll, part)},
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

// feishuComposeText appends the inline @-markers for Open IDs and the
// official "all" marker when at-all is on, plus any remaining (non Open-ID)
// user references, all together on their own trailing line: external Feishu
// groups only resolve Open IDs, and mentions render last (2026-09-15
// quick-fix: @用户/@所有人 放在最后单独一行).
func feishuComposeText(userIDs []string, atAll bool, body string) string {
	var markers strings.Builder
	var others []string
	if atAll {
		markers.WriteString("<at user_id=\"all\"></at> ")
	}
	for _, id := range userIDs {
		if strings.HasPrefix(id, "ou_") {
			markers.WriteString("<at user_id=\"" + id + "\"></at> ")
		} else {
			others = append(others, id)
		}
	}
	trailing := strings.TrimRight(markers.String(), " ")
	for _, id := range others {
		if trailing != "" {
			trailing += " "
		}
		trailing += "@" + id
	}
	if trailing == "" {
		return body
	}
	return body + "\n" + trailing
}

// feishuSign computes the Feishu custom-bot signature: base64 of
// HMAC-SHA256 keyed by the secret over "<unix-seconds>\n<secret>".
func feishuSign(secret string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

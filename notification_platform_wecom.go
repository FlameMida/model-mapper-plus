// notification_platform_wecom.go delivers markdown messages to WeCom group
// bots. WeCom supports no request signing: the webhook URL is called as-is
// and the business result is the errcode envelope — HTTP 200 with
// errcode != 0 is a delivery failure, never a forged success.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type wecomAdapter struct {
	client  *http.Client
	webhook string
	userIDs []string
}

func (a *wecomAdapter) send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error) {
	parts := splitMessage(msg.Body, wecomMarkdownLimit)
	results := make([]deliveryResult, 0, len(parts))
	for _, part := range parts {
		payload := map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]any{
				"content":        part,
				"mentioned_list": a.userIDs,
			},
		}
		status, respBody, retryAfter, err := postJSON(ctx, a.client, a.webhook, payload)
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
					ErrorCode: "wecom_errcode",
					Detail:    fmt.Sprintf("errcode=%d", env.Errcode),
				})
			} else {
				results = append(results, deliveryResult{Outcome: deliveryAccepted})
			}
		}
	}
	return results, nil
}

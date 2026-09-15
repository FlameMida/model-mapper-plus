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
	"strings"
)

type wecomAdapter struct {
	client  *http.Client
	webhook string
	userIDs []string
	atAll   bool
}

func (a *wecomAdapter) send(ctx context.Context, msg outboundMessage) ([]deliveryResult, error) {
	parts := splitMessage(msg.Body, wecomMarkdownLimit)
	results := make([]deliveryResult, 0, len(parts))
	for _, part := range parts {
		// @all only exists on msgtype=text (mentioned_list); WeCom markdown
		// has no at-all syntax, so at-all sends drop to plain text.
		var payload map[string]any
		if a.atAll {
			// text content carries no mention syntax; named mentions ride
			// mentioned_list alongside "@all".
			payload = map[string]any{
				"msgtype": "text",
				"text": map[string]any{
					"content":        part,
					"mentioned_list": append(append([]string{}, a.userIDs...), "@all"),
				},
			}
		} else {
			payload = map[string]any{
				"msgtype":  "markdown",
				"markdown": map[string]any{"content": wecomComposeMarkdown(a.userIDs, part)},
			}
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

// wecomComposeMarkdown appends the official <@userid> mention markers on
// their own trailing line (2026-09-15: @用户/@所有人 统一放在最后单独一行).
// The mentioned_list field is only defined for msgtype=text and is silently
// ignored on markdown, so markdown mentions must use the inline extension
// syntax in content instead.
func wecomComposeMarkdown(userIDs []string, body string) string {
	if len(userIDs) == 0 {
		return body
	}
	var b strings.Builder
	for _, id := range userIDs {
		b.WriteString("<@" + id + "> ")
	}
	return body + "\n" + strings.TrimRight(b.String(), " ")
}

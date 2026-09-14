// notification_platform_test.go covers the three platform adapters: request
// shape, business-error semantics (HTTP 200 with errcode/code != 0 is never
// accepted), signature presence, rate-limit handling and message splitting.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWeComSendRequestShape(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	ad, err := buildAdapter(PlatformIdentity{Kind: PlatformWeCom, Enabled: true, Webhook: srv.URL, UserIDs: []string{"u1"}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := ad.send(context.Background(), outboundMessage{Title: "t", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != deliveryAccepted {
		t.Fatalf("results: %+v", results)
	}
	md := gotBody["markdown"].(map[string]any)
	if gotBody["msgtype"] != "markdown" {
		t.Fatalf("msgtype: %v", gotBody["msgtype"])
	}
	// mentioned_list 仅在官方 text 类型参数表中定义，markdown 下静默忽略：
	// @ 必须走 content 内的 <@userid> 扩展语法。
	if _, exists := md["mentioned_list"]; exists {
		t.Fatal("markdown payload must not carry the text-only mentioned_list field")
	}
	content := md["content"].(string)
	if !strings.HasPrefix(content, "<@u1> ") || !strings.Contains(content, "hello") {
		t.Fatalf("content must prefix <@userid> markers then keep the body: %q", content)
	}
}

func TestWeComComposeMarkdownWithoutUserIDs(t *testing.T) {
	if got := wecomComposeMarkdown(nil, "plain body"); got != "plain body" {
		t.Fatalf("no user IDs must keep body untouched: %q", got)
	}
	if got := wecomComposeMarkdown([]string{"u1", "u2"}, "body"); got != "<@u1> <@u2> body" {
		t.Fatalf("each ID must become one <@userid> marker: %q", got)
	}
}

func TestWeComBusinessErrorNotAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook"}`))
	}))
	defer srv.Close()
	ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformWeCom, Enabled: true, Webhook: srv.URL})
	results, _ := ad.send(context.Background(), outboundMessage{Body: "x"})
	if results[0].Outcome == deliveryAccepted {
		t.Fatal("HTTP 200 with errcode!=0 must not be accepted (spec: 不伪造成功)")
	}
}

func TestFeishuSignPresentWhenSecretConfigured(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer srv.Close()
	ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformFeishu, Enabled: true, Webhook: srv.URL,
		SignSecret: "s3cr3t", UserIDs: []string{"ou_a"}})
	results, _ := ad.send(context.Background(), outboundMessage{Body: "hi"})
	if results[0].Outcome != deliveryAccepted {
		t.Fatalf("%+v", results)
	}
	if gotBody["sign"] == nil || gotBody["timestamp"] == nil {
		t.Fatal("signed request missing sign/timestamp")
	}
	if !strings.Contains(gotBody["content"].(map[string]any)["text"].(string), "<at user_id=\"ou_a\">") {
		t.Fatal("feishu text must inline open_id at-marker")
	}
}

func TestDingTalkRateLimitedYieldsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"errcode":130101,"errmsg":"limit"}`))
	}))
	defer srv.Close()
	ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformDingTalk, Enabled: true, Webhook: srv.URL + "?access_token=x", SignSecret: "sec"})
	results, _ := ad.send(context.Background(), outboundMessage{Body: "x"})
	if results[0].Outcome != deliveryFailed || results[0].ErrorCode != "rate_limited" || results[0].RetryAfter != 30*time.Second {
		t.Fatalf("rate limit: %+v", results[0])
	}
}

func TestWecomTransportErrorUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := srv.URL
	srv.Close() // 关闭替身模拟连接失败：HTTP 结果从未被观察到
	ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformWeCom, Enabled: true, Webhook: target})
	results, _ := ad.send(context.Background(), outboundMessage{Body: "x"})
	if len(results) != 1 || results[0].Outcome != deliveryUnknown || results[0].ErrorCode != "transport_unknown" {
		t.Fatalf("transport error must stay unknown, not failed: %+v", results)
	}
}

func TestSplitMessageRespectsSectionBoundary(t *testing.T) {
	sections := strings.Repeat("s\n\n", 3000) // 每节 ~3 字节 × 3000 = 9KB
	parts := splitMessage(strings.TrimRight(sections, "\n"), 4096)
	if len(parts) < 2 {
		t.Fatalf("expect split, got %d", len(parts))
	}
	for _, p := range parts {
		if len(p) > 4096 {
			t.Fatalf("part exceeds limit: %d", len(p))
		}
	}
}

// @所有人：飞书用官方 all 标记内联；钉钉 at.isAtAll；企微降级 text 类型携带 @all。
func TestAtAllPlatformShapes(t *testing.T) {
	t.Run("feishu inlines the official all marker", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &gotBody)
			w.Write([]byte(`{"code":0,"msg":"success"}`))
		}))
		defer srv.Close()
		ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformFeishu, Enabled: true, Webhook: srv.URL, AtAll: true})
		results, _ := ad.send(context.Background(), outboundMessage{Body: "hi"})
		if results[0].Outcome != deliveryAccepted {
			t.Fatalf("%+v", results)
		}
		text := gotBody["content"].(map[string]any)["text"].(string)
		if !strings.Contains(text, "<at user_id=\"all\"></at>") || !strings.Contains(text, "hi") {
			t.Fatalf("at-all text=%q", text)
		}
	})
	t.Run("dingtalk rides at.isAtAll", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &gotBody)
			w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		}))
		defer srv.Close()
		ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformDingTalk, Enabled: true, Webhook: srv.URL, AtAll: true})
		results, _ := ad.send(context.Background(), outboundMessage{Title: "t", Body: "hi"})
		if results[0].Outcome != deliveryAccepted {
			t.Fatalf("%+v", results)
		}
		if gotBody["at"].(map[string]any)["isAtAll"] != true {
			t.Fatalf("at=%v", gotBody["at"])
		}
	})
	t.Run("wecom drops to text with @all", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &gotBody)
			w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		}))
		defer srv.Close()
		ad, _ := buildAdapter(PlatformIdentity{Kind: PlatformWeCom, Enabled: true, Webhook: srv.URL, UserIDs: []string{"u1"}, AtAll: true})
		results, _ := ad.send(context.Background(), outboundMessage{Title: "t", Body: "hello"})
		if results[0].Outcome != deliveryAccepted {
			t.Fatalf("%+v", results)
		}
		if gotBody["msgtype"] != "text" {
			t.Fatalf("at-all must send text, got %v", gotBody["msgtype"])
		}
		list := gotBody["text"].(map[string]any)["mentioned_list"].([]any)
		if list[len(list)-1] != "@all" || list[0] != "u1" {
			t.Fatalf("mentioned_list=%v", list)
		}
		if !strings.Contains(gotBody["text"].(map[string]any)["content"].(string), "hello") {
			t.Fatalf("content=%v", gotBody["text"])
		}
	})
}

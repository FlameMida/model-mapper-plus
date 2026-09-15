package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func mgmtRequest(method, path, body string) pluginapi.ManagementRequest {
	var raw []byte
	if body != "" {
		raw = []byte(body)
	}
	return pluginapi.ManagementRequest{Method: method, Path: path, Body: raw, Query: map[string][]string{}}
}

func withTempNotificationState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	setLoadedConfigForTest(Config{Enabled: true, StateFile: filepath.Join(dir, "state.json"),
		UsageKeeperURL: "", UsageKeeperPasswordEnv: "X"})
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
}

// Scenario: 保存通知设置回显明文 Webhook/SignSecret（spec 2026-09-13）
func TestPutSettingsPersistsAndEchoesPlaintext(t *testing.T) {
	withTempNotificationState(t)
	body := `{"enabled":true,"global_default":{"id":"global","name":"用量通知","enabled":true,
	  "modules":[{"kind":"daily","period":"current"}],
	  "schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},
	  "platforms":[{"kind":"feishu","enabled":true,"webhook":"https://open.feishu.cn/hook/s3cr3t","user_ids":["ou_a"],"sign_secret":"sec1",
	    "fetch_app_id":"cli_x","fetch_app_secret":"app-sec-1"}]}}`
	resp := dispatchManagement(mgmtRequest(http.MethodPut, "/v0/management/plugins/model-mapper-plus/notifications/settings", body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", resp.StatusCode, resp.Body)
	}
	get := dispatchManagement(mgmtRequest(http.MethodGet, "/v0/management/plugins/model-mapper-plus/notifications/settings", ""))
	var out NotificationSettings
	if err := json.Unmarshal(get.Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.GlobalDefault.Platforms[0].Webhook != "https://open.feishu.cn/hook/s3cr3t" || out.GlobalDefault.Platforms[0].SignSecret != "sec1" {
		t.Fatalf("must echo plaintext webhook/secret, got %+v", out.GlobalDefault.Platforms)
	}
	if out.GlobalDefault.Platforms[0].FetchAppID != "cli_x" || out.GlobalDefault.Platforms[0].FetchAppSecret != "app-sec-1" {
		t.Fatalf("must echo plaintext fetch credentials, got %+v", out.GlobalDefault.Platforms[0])
	}
	if out.GlobalDefault.NextFire == "" {
		t.Fatal("settings must project next_fire")
	}
	if strings.Contains(out.GlobalDefault.NextFire, "T") || strings.Contains(out.GlobalDefault.NextFire, "+08") {
		t.Fatalf("next_fire must be human CST, got %q", out.GlobalDefault.NextFire)
	}
}

// Scenario: 全局启用平台可免填用户唯一 ID
func TestPutSettingsAllowsGlobalWithoutUserIDs(t *testing.T) {
	withTempNotificationState(t)
	body := `{"enabled":true,"global_default":{"id":"global","name":"用量通知","enabled":true,
	  "modules":[{"kind":"daily","period":"current"}],
	  "schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},
	  "platforms":[{"kind":"feishu","enabled":true,"webhook":"https://open.feishu.cn/hook/x"}]}}`
	resp := dispatchManagement(mgmtRequest(http.MethodPut, "/v0/management/plugins/model-mapper-plus/notifications/settings", body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("global may omit user_ids, got %d %s", resp.StatusCode, resp.Body)
	}
}

// Scenario: preview 用保存后的配置渲染且不发送；Keeper 未配置时产生受控警告而非 0 值
func TestPreviewRendersSavedConfigWithoutSending(t *testing.T) {
	withTempNotificationState(t)
	// 先保存与 T09-1 相同的合法 settings…
	body := `{"enabled":true,"global_default":{"id":"global","name":"用量通知","enabled":true,
	  "modules":[{"kind":"daily","period":"current"}],
	  "schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},
	  "platforms":[{"kind":"feishu","enabled":true,"webhook":"https://f","user_ids":["ou_a"]}]}}`
	if r := dispatchManagement(mgmtRequest(http.MethodPut, "/v0/management/plugins/model-mapper-plus/notifications/settings", body)); r.StatusCode != 200 {
		t.Fatalf("seed: %d %s", r.StatusCode, r.Body)
	}
	resp := dispatchManagement(mgmtRequest(http.MethodPost, "/v0/management/plugins/model-mapper-plus/notifications/preview", `{}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview: %d %s", resp.StatusCode, resp.Body)
	}
	var out struct {
		Text      string   `json:"text"`
		Warnings  []string `json:"warnings"`
		Bytes     int      `json:"bytes"`
		Platforms []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if !contains(out.Text, "用量通知") || out.Bytes == 0 {
		t.Fatalf("preview must render saved entity: %+v", out)
	}
	if len(out.Platforms) != 1 || out.Platforms[0].Kind != "feishu" || !contains(out.Platforms[0].Text, "用量通知") {
		t.Fatalf("preview must include enabled platforms: %+v", out.Platforms)
	}
	// Keeper 未配置（withTempNotificationState 里 URL=""）→ 预览含受控警告而非 0 值
	if len(out.Warnings) == 0 {
		t.Fatalf("unavailable keeper must produce warning, got %+v", out)
	}
}

// Scenario: 投递记录按 outcome 过滤，retry 以原 payload 重新入队 pending
func TestDeliveriesFilterAndRetry(t *testing.T) {
	withTempNotificationState(t)
	store := openTestStore(t) // 指向测试路径的通知库；生产路径由服务打开——测试直接注入
	now := time.Now()
	store.UpsertJob(notificationJob{ID: "j1", KeyFingerprint: "fp1", NotificationID: "global",
		Platform: PlatformFeishu, PeriodKey: "p", State: jobPending, Payload: []byte("hello"),
		NextAttempt: now, CreatedAt: now})
	store.FinishJob("j1", deliveryFailed, "rate_limited", "429")
	_ = store
	// 注入：notificationStoreForTest(store) 使管理 API 使用该 store（包内测试钩子，生产 nil）
	setNotificationStoreForTest(store)
	t.Cleanup(func() { setNotificationStoreForTest(nil) })
	resp := dispatchManagement(mgmtRequest(http.MethodGet,
		"/v0/management/plugins/model-mapper-plus/notifications/deliveries?outcome=failed", ""))
	var page struct {
		Items []deliveryRecord `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ErrorCode != "rate_limited" {
		t.Fatalf("filter: %+v", page.Items)
	}
	retry := dispatchManagement(mgmtRequest(http.MethodPost,
		"/v0/management/plugins/model-mapper-plus/notifications/deliveries/retry", `{"id":"`+page.Items[0].ID+`"}`))
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("retry: %d %s", retry.StatusCode, retry.Body)
	}
}

// Scenario: 保存通知设置纳入管理审计（spec：settings 保存走 auditedStateManagement）
func TestSettingsPutAudited(t *testing.T) {
	withTempNotificationState(t)
	body := `{"enabled":true,"global_default":{"id":"global","name":"用量通知","enabled":true,
	  "modules":[{"kind":"daily","period":"current"}],
	  "schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},
	  "platforms":[{"kind":"feishu","enabled":true,"webhook":"https://f","user_ids":["ou_a"]}]}}`
	resp := dispatchManagement(mgmtRequest(http.MethodPut, "/v0/management/plugins/model-mapper-plus/notifications/settings", body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", resp.StatusCode, resp.Body)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	raw, ok := out["audit"]
	if !ok {
		t.Fatalf("PUT settings response must carry audit metadata, got %s", resp.Body)
	}
	var meta struct {
		Recorded bool `json:"recorded"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("decode audit metadata: %v (%s)", err, raw)
	}
	if !meta.Recorded {
		t.Fatalf("audit must be recorded, got %s", raw)
	}
	if strings.Contains(string(resp.Body), "audit_unavailable") {
		t.Fatalf("unexpected audit failure: %s", resp.Body)
	}
}

// contains reports whether substr is in s (test-local helper for assertions).
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// 2026-09-14 确认：@所有人（at_all）仅全局通知语义；Key 级通知经管理保存时
// 后端剥离 at_all，旧数据下次保存自动清理，API 层无法再写入 key 级 @所有人。
func TestManagementPostKeyStripsKeyNotificationAtAll(t *testing.T) {
	withTempNotificationState(t)
	body := `{"key":"sk-k","enabled":true,"notifications":[{` +
		`"id":"n1","name":"日报用量","enabled":true,` +
		`"modules":[{"kind":"daily","period":"current"}],` +
		`"platforms":[{"kind":"wecom","enabled":true,"webhook":"https://qyapi.weixin.qq.com/hook","user_ids":["maverick"],"at_all":true}]}]}`
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(body)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("postKey: %d %s", resp.StatusCode, resp.Body)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 1 || len(st.KeyBindings[0].Notifications) != 1 {
		t.Fatalf("bindings = %+v", st.KeyBindings)
	}
	if st.KeyBindings[0].Notifications[0].Platforms[0].AtAll {
		t.Fatal("key-level notification at_all must be stripped on save")
	}
}

func TestManagementPostKeyRejectsKeyNotificationAtAllWithoutUserIDs(t *testing.T) {
	withTempNotificationState(t)
	body := `{"key":"sk-k","enabled":true,"notifications":[{` +
		`"id":"n1","name":"日报用量","enabled":true,` +
		`"modules":[{"kind":"daily","period":"current"}],` +
		`"platforms":[{"kind":"wecom","enabled":true,"webhook":"https://qyapi.weixin.qq.com/hook","at_all":true}]}]}`
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(body)})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("postKey: %d %s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "用户唯一 ID 为必填") {
		t.Fatalf("want user-id required after at_all strip, got %s", resp.Body)
	}
	if strings.Contains(string(resp.Body), "或开启") {
		t.Fatalf("key-level error must not suggest @所有人, got %s", resp.Body)
	}
}

func TestManagementPostKeyAllowsFollowGlobalWithoutModules(t *testing.T) {
	withTempNotificationState(t)
	body := `{"key":"sk-k","enabled":true,"notifications":[{` +
		`"id":"n1","name":"日报用量","enabled":true,"template_follows_global":true,` +
		`"platforms":[{"kind":"wecom","enabled":true,"webhook":"https://qyapi.weixin.qq.com/hook","user_ids":["maverick"]}]}]}`
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(body)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("follow-global without modules should save, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestPutSettingsPreservesGlobalAtAll(t *testing.T) {
	withTempNotificationState(t)
	body := `{"enabled":true,"notifications":[{"id":"g1","name":"每日通知","enabled":true,` +
		`"modules":[{"kind":"daily","period":"current"}],` +
		`"schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},` +
		`"platforms":[{"kind":"wecom","enabled":true,"webhook":"https://qyapi.weixin.qq.com/hook","at_all":true}]}]}`
	resp := dispatchManagement(mgmtRequest(http.MethodPut, "/v0/management/plugins/model-mapper-plus/notifications/settings", body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", resp.StatusCode, resp.Body)
	}
	got := dispatchManagement(mgmtRequest(http.MethodGet, "/v0/management/plugins/model-mapper-plus/notifications/settings", ""))
	if !strings.Contains(string(got.Body), `"at_all":true`) {
		t.Fatalf("global at_all must persist, got %s", got.Body)
	}
}

// 2026-09-15 quick-fix：全局预览每块正文按渠道渲染（与到期发送一致）——
// 展示名取该渠道的 Keeper 命名而非恒为「未知/未提供」，统计只含该渠道。
func TestStackedPreviewRendersPerChannelDisplayName(t *testing.T) {
	withTempNotificationState(t)
	// 预览的统计源需要一个非 nil service；client 为 nil 的 source 让
	// 累计统计走 _statsCollector stub，配额降级链安静退化。
	prevSvc := activeNotification.svc
	activeNotification.svc = &notificationService{deps: serviceDeps{Source: &keeperStatsSource{now: time.Now}}}
	t.Cleanup(func() { activeNotification.svc = prevSvc })
	prevCollector := _statsCollector
	_statsCollector = func(_ *keeperStatsSource, _ context.Context, _ string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error) {
		return periodStats{
			PeriodKey: periodKeyOf(kind, period, now),
			Channels: []channelStats{
				{Name: "ai_1", Label: "Codex", Identity: "ai_1", Tokens: 800, ShareKnown: false},
				{Name: "ai_2", Label: "Claude", Identity: "ai_2", Tokens: 200, ShareKnown: false},
			},
		}, nil
	}
	t.Cleanup(func() { _statsCollector = prevCollector })
	prevCh := _testChannels
	_ = prevCh
	_testChannels = []string{"ai_1", "ai_2"}
	t.Cleanup(func() { _testChannels = prevCh })

	n := Notification{Name: "日报用量", Enabled: true,
		Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}}}
	text, warnings := stackedPreview(nil, n)
	if !contains(text, "Codex ·") || !contains(text, "Claude ·") {
		t.Fatalf("channel display name must replace 未知/未提供: %q (warnings=%v)", text, warnings)
	}
	// 每块只渲染自己渠道的统计行：800 只在 ai_1 块，200 只在 ai_2 块。
	if got := strings.Count(text, "用量 800 tokens"); got != 1 {
		t.Fatalf("ai_1 stats must render once, got %d: %q", got, text)
	}
	if got := strings.Count(text, "用量 200 tokens"); got != 1 {
		t.Fatalf("ai_2 stats must render once, got %d: %q", got, text)
	}
}

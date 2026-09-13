package main

import (
	"strings"
	"testing"
)

func validNotification() Notification {
	return Notification{
		ID: "n1", Name: "日报用量", Enabled: true,
		Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}},
		Schedule: &NotificationSchedule{Kind: ScheduleInterval, Interval: 86400, Time: "09:00:00"},
		Platforms: []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true,
			Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/x", UserIDs: []string{"ou_a"}}},
	}
}

func TestValidateKeyNotificationsNameUnique(t *testing.T) {
	st := State{Notifications: &NotificationSettings{Enabled: true, GlobalDefault: Notification{ID: GlobalNotificationID, Name: "用量通知", Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}}, Platforms: []PlatformIdentity{}}}}
	dup := validNotification()
	dup2 := validNotification()
	dup2.Name = " 日报用量 " // trim 后同名
	st.KeyBindings = []KeyBinding{{Key: "k1", Notifications: []Notification{dup, dup2}}}
	err := validateKeyNotifications(&st)
	if err == nil || !strings.Contains(err.Error(), "key_bindings[0].notifications[1]") {
		t.Fatalf("want duplicate-name error anchored at index, got %v", err)
	}
}

func TestValidateEnabledPlatformRequiresIdentity(t *testing.T) {
	n := validNotification()
	n.Platforms = []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://x", UserIDs: nil}}
	st := State{KeyBindings: []KeyBinding{{Key: "k1", Notifications: []Notification{n}}}}
	err := validateKeyNotifications(&st)
	if err == nil || !strings.Contains(err.Error(), "user_ids") {
		t.Fatalf("want missing user_ids error, got %v", err)
	}
}

func TestValidateWeComRejectsSignSecret(t *testing.T) {
	n := validNotification()
	n.Platforms = []PlatformIdentity{{Kind: PlatformWeCom, Enabled: true,
		Webhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=1", UserIDs: []string{"u1"}, SignSecret: "s"}}
	st := State{KeyBindings: []KeyBinding{{Key: "k1", Notifications: []Notification{n}}}}
	if err := validateKeyNotifications(&st); err == nil || !strings.Contains(err.Error(), "sign_secret") {
		t.Fatalf("wecom must not carry sign_secret, got %v", err)
	}
}

func TestEffectiveNotificationsGlobalFallback(t *testing.T) {
	global := Notification{ID: GlobalNotificationID, Name: "用量通知", Enabled: true,
		Modules: []ModuleConfig{{Kind: ModuleWeeklyWindow}}, Platforms: []PlatformIdentity{}}
	st := State{Notifications: &NotificationSettings{Enabled: true, GlobalDefault: global}}
	empty := &KeyBinding{Key: "k"}
	got := effectiveNotifications(&st, empty)
	if len(got) != 1 || got[0].ID != GlobalNotificationID {
		t.Fatalf("no-notification key must fall back to global entity, got %v", got)
	}
	withOwn := &KeyBinding{Key: "k", Notifications: []Notification{validNotification()}}
	got = effectiveNotifications(&st, withOwn)
	if len(got) != 1 || got[0].ID != "n1" {
		t.Fatalf("own notifications must fully replace global, got %v", got)
	}
}

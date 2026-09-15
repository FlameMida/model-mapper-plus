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

func TestGlobalEnabledPlatformMayOmitUserIDs(t *testing.T) {
	n := validNotification()
	n.Platforms = []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://x"}}
	if err := validateNotificationEntity(&n, "notifications[0]", true); err != nil {
		t.Fatalf("global may omit user ids: %v", err)
	}
}

func TestValidateEnabledPlatformRequiresIdentity(t *testing.T) {
	n := validNotification()
	n.Platforms = []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://x", UserIDs: nil}}
	st := State{KeyBindings: []KeyBinding{{Key: "k1", Notifications: []Notification{n}}}}
	err := validateKeyNotifications(&st)
	if err == nil || !strings.Contains(err.Error(), "用户唯一 ID") {
		t.Fatalf("want missing user_ids error, got %v", err)
	}
}

func TestValidateWeComRejectsSignSecret(t *testing.T) {
	n := validNotification()
	n.Platforms = []PlatformIdentity{{Kind: PlatformWeCom, Enabled: true,
		Webhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=1", UserIDs: []string{"u1"}, SignSecret: "s"}}
	st := State{KeyBindings: []KeyBinding{{Key: "k1", Notifications: []Notification{n}}}}
	if err := validateKeyNotifications(&st); err == nil || !strings.Contains(err.Error(), "签名密钥") {
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

// —— 全局多通知（多条全局默认列表 + is_default 标记）——

func globalMultiSettings() *NotificationSettings {
	base := validNotification()
	return &NotificationSettings{Enabled: true, Notifications: []Notification{
		{ID: "g1", Name: "日报", Enabled: true, IsDefault: true, Modules: base.Modules,
			Schedule: &NotificationSchedule{Kind: "interval", Interval: 86400, Time: "09:00:00"},
			Platforms: base.Platforms},
		{ID: "g2", Name: "月报", Enabled: true, Modules: base.Modules,
			Schedule: &NotificationSchedule{Kind: "monthly", Day: 1, Time: "10:00:00"},
			Platforms: base.Platforms},
	}}
}

// 旧单条数据（只有 global_default）必须迁移为列表第一条并标记默认。
func TestNormalizeMigratesLegacyGlobalDefault(t *testing.T) {
	s := &NotificationSettings{Enabled: true, GlobalDefault: validNotification()}
	normalizeNotificationSettings(s)
	if len(s.Notifications) != 1 || !s.Notifications[0].IsDefault {
		t.Fatalf("legacy global_default must migrate to a one-entry default list: %+v", s.Notifications)
	}
	if s.GlobalDefault.ID != s.Notifications[0].ID {
		t.Fatalf("global_default must stay in sync with the default entry: %+v", s.GlobalDefault)
	}
}

// is_default 归一：恰一个标记存活——有标记时保留第一条被标记的，否则第一条。
func TestNormalizePinsExactlyOneDefault(t *testing.T) {
	s := globalMultiSettings()
	s.Notifications[0].IsDefault = false
	s.Notifications[1].IsDefault = true
	normalizeNotificationSettings(s)
	if s.Notifications[0].IsDefault || !s.Notifications[1].IsDefault {
		t.Fatalf("the flagged entry must keep the pin: %+v", s.Notifications)
	}
	s = globalMultiSettings()
	s.Notifications[1].IsDefault = true
	normalizeNotificationSettings(s)
	if !s.Notifications[0].IsDefault || s.Notifications[1].IsDefault || s.GlobalDefault.ID != "g1" {
		t.Fatalf("exactly one default must survive: %+v / gd=%+v", s.Notifications, s.GlobalDefault)
	}
	// 全局条自身的「跟随全局」标记没有意义，归一化清除。
	s.Notifications[0].TemplateFollowsGlobal = true
	s.Notifications[0].ScheduleFollowsGlobal = true
	normalizeNotificationSettings(s)
	if s.Notifications[0].TemplateFollowsGlobal || s.Notifications[0].ScheduleFollowsGlobal {
		t.Fatal("global entries must never follow the global default")
	}
}

// 校验：列表名称与 ID 唯一；实体规则逐条生效。
func TestValidateGlobalListUniqueness(t *testing.T) {
	s := globalMultiSettings()
	s.Notifications[1].Name = "日报"
	if err := validateNotificationSettings(s); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate names must fail, got %v", err)
	}
	s = globalMultiSettings()
	s.Notifications[1].ID = "g1"
	if err := validateNotificationSettings(s); err == nil || !strings.Contains(err.Error(), "ID") {
		t.Fatalf("duplicate ids must fail, got %v", err)
	}
}

// key 级空列表回落 = 整个全局多条列表；自有列表仍整体替换。
func TestEffectiveNotificationsGlobalListFallback(t *testing.T) {
	st := State{Notifications: globalMultiSettings()}
	got := effectiveNotifications(&st, &KeyBinding{Key: "k"})
	if len(got) != 2 || got[0].ID != "g1" || got[1].ID != "g2" {
		t.Fatalf("empty key list must fall back to the whole global list: %+v", got)
	}
	own := &KeyBinding{Key: "k", Notifications: []Notification{validNotification()}}
	if got := effectiveNotifications(&st, own); len(got) != 1 || got[0].ID != "n1" {
		t.Fatalf("own list must fully replace, got %+v", got)
	}
}

// 跟随全局的解析：跟随标记条；不跟随时用自身字段；全局条不跟随。
func TestEffectiveTemplateAndScheduleFollowDefault(t *testing.T) {
	st := State{Notifications: globalMultiSettings()}
	follower := validNotification()
	follower.ID = "n9"
	follower.TemplateFollowsGlobal = true
	follower.ScheduleFollowsGlobal = true
	follower.Modules = nil
	follower.Schedule = nil
	if m := effectiveModules(&st, follower); len(m) != len(globalMultiSettings().Notifications[0].Modules) {
		t.Fatalf("template follower must take the default entry modules: %+v", m)
	}
	if s := effectiveSchedule(&st, follower); s == nil || s.Kind != "interval" {
		t.Fatalf("schedule follower must take the default entry schedule: %+v", s)
	}
	own := validNotification()
	own.Schedule = &NotificationSchedule{Kind: "monthly", Time: "08:00:00"}
	if s := effectiveSchedule(&st, own); s.Kind != "monthly" {
		t.Fatalf("non-follower keeps its own schedule: %+v", s)
	}
	// 全局条（无 key）永远用自身字段。
	if m := effectiveModules(&st, globalMultiSettings().Notifications[1]); len(m) == 0 {
		t.Fatal("global entry must keep its own modules")
	}
}

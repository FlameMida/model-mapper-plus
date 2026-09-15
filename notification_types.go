package main

import (
	"fmt"
	"strconv"
	"strings"
)

// PlatformKind identifies a group-bot delivery platform.
type PlatformKind string

const (
	PlatformWeCom    PlatformKind = "wecom"
	PlatformFeishu   PlatformKind = "feishu"
	PlatformDingTalk PlatformKind = "dingtalk"
)

// ModuleKind selects a statistics module rendered into the notification body.
type ModuleKind string

const (
	ModuleDaily        ModuleKind = "daily"
	ModuleMonthly      ModuleKind = "monthly"
	ModuleHalfYear     ModuleKind = "half_year"
	ModuleYearly       ModuleKind = "yearly"
	ModuleWindow5H     ModuleKind = "window_5h"
	ModuleWeeklyWindow ModuleKind = "weekly"
	ModuleResetCards   ModuleKind = "reset_cards"
)

// PeriodKind selects current accumulating period vs previous complete period.
type PeriodKind string

const (
	PeriodCurrent  PeriodKind = "current"
	PeriodPrevious PeriodKind = "previous"
)

// ScheduleKind selects between fixed-interval and calendar schedules.
type ScheduleKind string

const (
	ScheduleInterval ScheduleKind = "interval"
	ScheduleMonthly  ScheduleKind = "monthly"
	ScheduleYearly   ScheduleKind = "yearly"
)

// GlobalNotificationID is the reserved ID of the global default notification.
const GlobalNotificationID = "global"

// NotificationSettings is the global notification block persisted in State.
// Notifications is the source of truth (multi-entry since v0.6.0); the legacy
// single GlobalDefault is kept in sync with the is_default entry so older
// readers still work.
type NotificationSettings struct {
	Enabled       bool          `json:"enabled"`
	GlobalDefault Notification  `json:"global_default"`
	Notifications []Notification `json:"notifications,omitempty"`
}

// Notification is one independent send unit: template modules, schedule and
// platform identities. Key-level notifications fully replace the global list.
// IsDefault pins the global entry that key-level followers resolve against.
type Notification struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Enabled               bool                  `json:"enabled"`
	IsDefault             bool                  `json:"is_default,omitempty"`
	TemplateFollowsGlobal bool                  `json:"template_follows_global"`
	ScheduleFollowsGlobal bool                  `json:"schedule_follows_global"`
	Modules               []ModuleConfig        `json:"modules"`
	Schedule              *NotificationSchedule `json:"schedule"`
	Platforms             []PlatformIdentity    `json:"platforms"`
	NextFire              string                `json:"next_fire,omitempty"`
}

// ModuleConfig pairs a statistics module with its period; window modules
// (5H/weekly/reset cards) carry no period.
type ModuleConfig struct {
	Kind   ModuleKind `json:"kind"`
	Period PeriodKind `json:"period"`
}

// NotificationSchedule is the send plan: fixed interval seconds or a calendar
// time (monthly month-end toggle, yearly month/day/time).
type NotificationSchedule struct {
	Kind     ScheduleKind `json:"kind"`
	Interval int          `json:"interval"`
	MonthEnd bool         `json:"month_end"`
	Month    int          `json:"month"`
	Day      int          `json:"day"`
	Time     string       `json:"time"`
}

// PlatformIdentity is one platform target: webhook, @-user IDs and the
// signature secret where the platform supports one (DingTalk, Feishu). The
// fetch_* credential fields are optional directory-fetch credentials used
// only by the member-picker (feishu: app id/secret, dingtalk: app key plus
// the shared app secret, wecom: corp id/secret); they never join delivery.
type PlatformIdentity struct {
	Kind           PlatformKind `json:"kind"`
	Enabled        bool         `json:"enabled"`
	Webhook        string       `json:"webhook"`
	UserIDs        []string     `json:"user_ids"`
	AtAll          bool         `json:"at_all,omitempty"`
	SignSecret     string       `json:"sign_secret"`
	FetchAppID     string       `json:"fetch_app_id"`
	FetchAppSecret string       `json:"fetch_app_secret"`
	FetchAppKey    string       `json:"fetch_app_key"`
	FetchCorpID    string       `json:"fetch_corp_id"`
	FetchSecret    string       `json:"fetch_secret"`
}

var validModuleKinds = map[ModuleKind]bool{
	ModuleDaily: true, ModuleMonthly: true, ModuleHalfYear: true, ModuleYearly: true,
	ModuleWindow5H: true, ModuleWeeklyWindow: true, ModuleResetCards: true,
}

// moduleKindsWithPeriod are the cumulative-statistics modules that must carry
// a current/previous period; window modules must leave it empty.
var moduleKindsWithPeriod = map[ModuleKind]bool{
	ModuleDaily: true, ModuleMonthly: true, ModuleHalfYear: true, ModuleYearly: true,
}

var validPlatformKinds = map[PlatformKind]bool{
	PlatformWeCom: true, PlatformFeishu: true, PlatformDingTalk: true,
}

// maxScheduleIntervalSeconds caps fixed intervals at 31 days.
const maxScheduleIntervalSeconds = 31 * 86400

// validDayTime reports whether s is a valid "HH:MM:SS" wall-clock string.
// Yearly 2/29 validity is left to the scheduler (invalid dates are skipped).
func validDayTime(s string) error {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return fmt.Errorf("时间格式需为 HH:MM:SS")
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if len(p) != 2 {
			return fmt.Errorf("时间格式需为 HH:MM:SS")
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return fmt.Errorf("时间格式需为 HH:MM:SS")
		}
		nums[i] = v
	}
	if nums[0] > 23 || nums[1] > 59 || nums[2] > 59 {
		return fmt.Errorf("时间需为有效的 HH:MM:SS 时刻")
	}
	return nil
}

// validateNotificationSchedule enforces the per-kind field contract: interval
// keeps seconds in range (its time, when present, is an optional send anchor);
// monthly keeps a send time; yearly adds month/day ranges.
func validateNotificationSchedule(s *NotificationSchedule, path string) error {
	switch s.Kind {
	case ScheduleInterval:
		if s.Interval < 1 || s.Interval > maxScheduleIntervalSeconds {
			return fmt.Errorf("%s: 间隔需在 1 到 %d 秒之间", path, maxScheduleIntervalSeconds)
		}
		if s.Time != "" {
			if err := validDayTime(s.Time); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	case ScheduleMonthly:
		if err := validDayTime(s.Time); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case ScheduleYearly:
		if s.Month < 1 || s.Month > 12 {
			return fmt.Errorf("%s: 月份需在 1 到 12 之间", path)
		}
		if s.Day < 1 || s.Day > 31 {
			return fmt.Errorf("%s: 日期需在 1 到 31 之间", path)
		}
		if err := validDayTime(s.Time); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	default:
		return fmt.Errorf("%s: 未知的计划类型 %q", path, string(s.Kind))
	}
	return nil
}

// validateNotificationEntity validates one notification entity (global default
// or a key-owned entry); every error is anchored at path. allowAtAll is true
// only for global notifications: @所有人 is global-only (2026-09-14).
func validateNotificationEntity(n *Notification, path string, allowAtAll bool) error {
	if strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("%s: 通知名称为必填", path)
	}
	// Key-level followers inherit the pinned global template; they may omit
	// modules. Global entries never carry the follow flag (normalize clears it).
	if len(n.Modules) == 0 && !n.TemplateFollowsGlobal {
		return fmt.Errorf("%s: 至少启用一个统计模块", path)
	}
	seenModules := make(map[ModuleKind]struct{}, len(n.Modules))
	for i := range n.Modules {
		m := &n.Modules[i]
		if !validModuleKinds[m.Kind] {
			return fmt.Errorf("%s.modules[%d]: 未知的统计模块 %q", path, i, string(m.Kind))
		}
		if _, dup := seenModules[m.Kind]; dup {
			return fmt.Errorf("%s.modules[%d]: 统计模块 %q 重复", path, i, string(m.Kind))
		}
		seenModules[m.Kind] = struct{}{}
		if moduleKindsWithPeriod[m.Kind] {
			switch m.Period {
			case PeriodCurrent, PeriodPrevious:
			default:
				return fmt.Errorf("%s.modules[%d]: 统计模块 %q 需选择「本期累计」或「上一完整周期」", path, i, string(m.Kind))
			}
		} else if m.Period != "" {
			return fmt.Errorf("%s.modules[%d]: 模块 %q 无需选择统计周期", path, i, string(m.Kind))
		}
	}
	if n.Schedule != nil {
		if err := validateNotificationSchedule(n.Schedule, path+".schedule"); err != nil {
			return err
		}
	}
	seenPlatforms := make(map[PlatformKind]struct{}, len(n.Platforms))
	for i := range n.Platforms {
		p := &n.Platforms[i]
		if !validPlatformKinds[p.Kind] {
			return fmt.Errorf("%s.platforms[%d]: 未知的平台 %q", path, i, string(p.Kind))
		}
		if _, dup := seenPlatforms[p.Kind]; dup {
			return fmt.Errorf("%s.platforms[%d]: 平台 %q 重复配置", path, i, string(p.Kind))
		}
		seenPlatforms[p.Kind] = struct{}{}
		if p.Enabled {
			if !strings.HasPrefix(p.Webhook, "http://") && !strings.HasPrefix(p.Webhook, "https://") {
				return fmt.Errorf("%s.platforms[%d]: Webhook 地址需以 http:// 或 https:// 开头", path, i)
			}
			hasUserID := false
			for _, id := range p.UserIDs {
				if strings.TrimSpace(id) != "" {
					hasUserID = true
					break
				}
			}
			if !hasUserID && !allowAtAll {
				return fmt.Errorf("%s.platforms[%d]: 启用通知时用户唯一 ID 为必填", path, i)
			}
		}
		if p.Kind == PlatformWeCom && strings.TrimSpace(p.SignSecret) != "" {
			return fmt.Errorf("%s.platforms[%d]: 企业微信不支持签名密钥", path, i)
		}
	}
	return nil
}

// normalizeNotificationSettings migrates and pins the global notification
// list: a legacy single global_default becomes the one-entry list; exactly
// one entry keeps the is_default pin (first wins); global_default stays in
// sync with the pinned entry; global entries never carry follow-global flags.
func normalizeNotificationSettings(s *NotificationSettings) {
	if len(s.Notifications) == 0 {
		s.Notifications = []Notification{s.GlobalDefault}
	}
	// Exactly one pin survives: the first flagged entry, else the first entry.
	defaultIdx := 0
	for i := range s.Notifications {
		if s.Notifications[i].IsDefault {
			defaultIdx = i
			break
		}
	}
	for i := range s.Notifications {
		s.Notifications[i].IsDefault = i == defaultIdx
		s.Notifications[i].TemplateFollowsGlobal = false
		s.Notifications[i].ScheduleFollowsGlobal = false
	}
	s.GlobalDefault = s.Notifications[defaultIdx]
}

// defaultGlobalNotification returns the pinned global entry key-level
// followers resolve against, or nil when nothing is configured.
func defaultGlobalNotification(s *NotificationSettings) *Notification {
	if s == nil {
		return nil
	}
	for i := range s.Notifications {
		if s.Notifications[i].IsDefault {
			return &s.Notifications[i]
		}
	}
	if len(s.Notifications) == 0 {
		return nil
	}
	return &s.Notifications[0]
}

// effectiveModules resolves the modules a render should use: followers take
// the pinned global entry's template, everything else its own.
func effectiveModules(st *State, n Notification) []ModuleConfig {
	mods := n.Modules
	if n.TemplateFollowsGlobal && st != nil && st.Notifications != nil {
		if d := defaultGlobalNotification(st.Notifications); d != nil {
			mods = d.Modules
		}
	}
	return orderModulesByGroup(mods)
}

// orderModulesByGroup matches the editor's 统计周期 / 渠道窗口 sections:
// period modules keep their relative order, then window/reset-card modules.
// Checking a window module must not hoist it above daily/monthly stats.
func orderModulesByGroup(mods []ModuleConfig) []ModuleConfig {
	if len(mods) < 2 {
		return mods
	}
	stats := make([]ModuleConfig, 0, len(mods))
	windows := make([]ModuleConfig, 0, len(mods))
	for _, m := range mods {
		if moduleKindsWithPeriod[m.Kind] {
			stats = append(stats, m)
			continue
		}
		windows = append(windows, m)
	}
	if len(stats) == 0 || len(windows) == 0 {
		return mods
	}
	return append(stats, windows...)
}

// effectiveSchedule resolves the schedule a tick should fire: followers take
// the pinned global entry's plan, everything else its own.
func effectiveSchedule(st *State, n Notification) *NotificationSchedule {
	if n.ScheduleFollowsGlobal && st != nil && st.Notifications != nil {
		if d := defaultGlobalNotification(st.Notifications); d != nil {
			return d.Schedule
		}
	}
	return n.Schedule
}

// validateNotificationSettings validates the global notification list.
func validateNotificationSettings(s *NotificationSettings) error {
	seenNames := map[string]int{}
	seenIDs := map[string]int{}
	for i := range s.Notifications {
		n := &s.Notifications[i]
		path := fmt.Sprintf("notifications.notifications[%d]", i)
		if strings.TrimSpace(n.Name) == "" {
			return fmt.Errorf("%s: 通知名称为必填", path)
		}
		if err := validateNotificationEntity(n, path, true); err != nil {
			return err
		}
		name := strings.TrimSpace(n.Name)
		if prev, exists := seenNames[name]; exists {
			return fmt.Errorf("%s: 通知名称 %q 与第 %d 条通知重复", path, name, prev)
		}
		seenNames[name] = i
		if id := strings.TrimSpace(n.ID); id != "" {
			if prev, exists := seenIDs[id]; exists {
				return fmt.Errorf("%s: 通知 ID %q 与第 %d 条通知重复", path, id, prev)
			}
			seenIDs[id] = i
		}
	}
	if len(s.Notifications) == 0 {
		return fmt.Errorf("notifications.notifications: 至少保留一条全局通知")
	}
	return nil
}

// stripKeyNotificationAtAll clears @所有人 from a binding's notification list
// before it is persisted: at_all is global-only semantics (2026-09-14), so
// key-level entries lose the flag on save and legacy data self-cleans on the
// next save instead of failing validation.
func stripKeyNotificationAtAll(b *KeyBinding) {
	for i := range b.Notifications {
		for j := range b.Notifications[i].Platforms {
			b.Notifications[i].Platforms[j].AtAll = false
		}
	}
}

// validateKeyNotificationsEntity validates one binding's own notification
// list, anchored at key_bindings[<index>].notifications[<j>], including
// trim-based name uniqueness inside the binding. An empty list is valid (the
// binding falls back to the global default notification).
func validateKeyNotificationsEntity(index int, b *KeyBinding) error {
	if len(b.Notifications) == 0 {
		return nil
	}
	seenNames := make(map[string]int, len(b.Notifications))
	for j := range b.Notifications {
		n := &b.Notifications[j]
		path := fmt.Sprintf("key_bindings[%d].notifications[%d]", index, j)
		if err := validateNotificationEntity(n, path, false); err != nil {
			return err
		}
		name := strings.TrimSpace(n.Name)
		if prev, exists := seenNames[name]; exists {
			return fmt.Errorf("%s: 通知名称 %q 与第 %d 条通知重复", path, name, prev)
		}
		seenNames[name] = j
	}
	return nil
}

// validateKeyNotifications validates every binding's notification list.
func validateKeyNotifications(st *State) error {
	for i := range st.KeyBindings {
		if err := validateKeyNotificationsEntity(i, &st.KeyBindings[i]); err != nil {
			return err
		}
	}
	return nil
}

// effectiveNotifications returns the notifications governing a binding:
// its own list when non-empty (fully replacing the global list), otherwise
// the whole global notification list. Copies are returned so callers cannot
// mutate persisted state through the result.
func effectiveNotifications(st *State, binding *KeyBinding) []Notification {
	if len(binding.Notifications) > 0 {
		return cloneNotifications(binding.Notifications)
	}
	if st.Notifications == nil {
		return nil
	}
	// Legacy states (or in-memory snapshots) may still carry only the single
	// global_default; fall back to it so nothing resolves to an empty list.
	if len(st.Notifications.Notifications) > 0 {
		return cloneNotifications(st.Notifications.Notifications)
	}
	return cloneNotifications([]Notification{st.Notifications.GlobalDefault})
}

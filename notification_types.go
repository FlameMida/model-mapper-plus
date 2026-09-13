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
type NotificationSettings struct {
	Enabled       bool         `json:"enabled"`
	GlobalDefault Notification `json:"global_default"`
}

// Notification is one independent send unit: template modules, schedule and
// platform identities. Key-level notifications fully replace the global one.
type Notification struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Enabled               bool                  `json:"enabled"`
	TemplateFollowsGlobal bool                  `json:"template_follows_global"`
	ScheduleFollowsGlobal bool                  `json:"schedule_follows_global"`
	Modules               []ModuleConfig        `json:"modules"`
	Schedule              *NotificationSchedule `json:"schedule"`
	Platforms             []PlatformIdentity    `json:"platforms"`
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
// signature secret where the platform supports one (DingTalk, Feishu).
type PlatformIdentity struct {
	Kind       PlatformKind `json:"kind"`
	Enabled    bool         `json:"enabled"`
	Webhook    string       `json:"webhook"`
	UserIDs    []string     `json:"user_ids"`
	SignSecret string       `json:"sign_secret"`
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
		return fmt.Errorf("time must be HH:MM:SS")
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if len(p) != 2 {
			return fmt.Errorf("time must be HH:MM:SS")
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return fmt.Errorf("time must be HH:MM:SS")
		}
		nums[i] = v
	}
	if nums[0] > 23 || nums[1] > 59 || nums[2] > 59 {
		return fmt.Errorf("time must be a valid HH:MM:SS clock")
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
			return fmt.Errorf("%s: interval must be between 1 and %d seconds", path, maxScheduleIntervalSeconds)
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
			return fmt.Errorf("%s: month must be between 1 and 12", path)
		}
		if s.Day < 1 || s.Day > 31 {
			return fmt.Errorf("%s: day must be between 1 and 31", path)
		}
		if err := validDayTime(s.Time); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	default:
		return fmt.Errorf("%s: unknown schedule kind %q", path, string(s.Kind))
	}
	return nil
}

// validateNotificationEntity validates one notification entity (global default
// or a key-owned entry); every error is anchored at path.
func validateNotificationEntity(n *Notification, path string) error {
	if strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("%s: name is required", path)
	}
	if len(n.Modules) == 0 {
		return fmt.Errorf("%s: modules is required", path)
	}
	seenModules := make(map[ModuleKind]struct{}, len(n.Modules))
	for i := range n.Modules {
		m := &n.Modules[i]
		if !validModuleKinds[m.Kind] {
			return fmt.Errorf("%s.modules[%d]: unknown module kind %q", path, i, string(m.Kind))
		}
		if _, dup := seenModules[m.Kind]; dup {
			return fmt.Errorf("%s.modules[%d]: duplicate module kind %q", path, i, string(m.Kind))
		}
		seenModules[m.Kind] = struct{}{}
		if moduleKindsWithPeriod[m.Kind] {
			switch m.Period {
			case PeriodCurrent, PeriodPrevious:
			default:
				return fmt.Errorf("%s.modules[%d]: period must be current or previous for module %q", path, i, string(m.Kind))
			}
		} else if m.Period != "" {
			return fmt.Errorf("%s.modules[%d]: period must be empty for module %q", path, i, string(m.Kind))
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
			return fmt.Errorf("%s.platforms[%d]: unknown platform kind %q", path, i, string(p.Kind))
		}
		if _, dup := seenPlatforms[p.Kind]; dup {
			return fmt.Errorf("%s.platforms[%d]: duplicate platform kind %q", path, i, string(p.Kind))
		}
		seenPlatforms[p.Kind] = struct{}{}
		if p.Enabled {
			if !strings.HasPrefix(p.Webhook, "http://") && !strings.HasPrefix(p.Webhook, "https://") {
				return fmt.Errorf("%s.platforms[%d]: webhook must start with http:// or https://", path, i)
			}
			hasUserID := false
			for _, id := range p.UserIDs {
				if strings.TrimSpace(id) != "" {
					hasUserID = true
					break
				}
			}
			if !hasUserID {
				return fmt.Errorf("%s.platforms[%d]: user_ids is required", path, i)
			}
		}
		if p.Kind == PlatformWeCom && strings.TrimSpace(p.SignSecret) != "" {
			return fmt.Errorf("%s.platforms[%d]: sign_secret not supported for wecom", path, i)
		}
	}
	return nil
}

// validateNotificationSettings validates the global default notification.
func validateNotificationSettings(s *NotificationSettings) error {
	if strings.TrimSpace(s.GlobalDefault.Name) == "" {
		return fmt.Errorf("notifications.global_default: name is required")
	}
	return validateNotificationEntity(&s.GlobalDefault, "notifications.global_default")
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
		if err := validateNotificationEntity(n, path); err != nil {
			return err
		}
		name := strings.TrimSpace(n.Name)
		if prev, exists := seenNames[name]; exists {
			return fmt.Errorf("%s: duplicate notification name %q (same as notifications[%d])", path, name, prev)
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
// its own list when non-empty (fully replacing the global default), otherwise
// a single-element slice with the global default entity. Copies are returned
// so callers cannot mutate persisted state through the result.
func effectiveNotifications(st *State, binding *KeyBinding) []Notification {
	if len(binding.Notifications) > 0 {
		return cloneNotifications(binding.Notifications)
	}
	if st.Notifications == nil {
		return nil
	}
	return cloneNotifications([]Notification{st.Notifications.GlobalDefault})
}

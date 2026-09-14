// notification_members_client.go fetches directory members (name + unique
// ID) for the admin member picker. Feishu rides the official oapi-sdk-go
// (token management, pagination, typed contact models). DingTalk and WeCom
// publish no official Go SDK — their directory surface is three plain REST
// endpoints each, implemented here over the shared keeper error discipline:
// closed error codes only, bounded bodies, 5s per request, capped traversal.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

// notificationMember is one directory row: display name plus the platform
// unique ID that @-mentions require (feishu ou_ open_id, dingtalk userid,
// wecom userid).
type notificationMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Traversal safety caps so a broken or hostile upstream cannot loop the
// pagination or balloon the member slice.
const (
	maxMemberRequests = 5000
	maxMemberCount    = 20000
)

var (
	dingtalkTokenURL = "https://api.dingtalk.com/v1.0/oauth2/accessToken"
	dingtalkDeptURL  = "https://oapi.dingtalk.com/topapi/v2/department/listsub"
	dingtalkUsersURL = "https://oapi.dingtalk.com/topapi/v2/user/list"
	wecomTokenURL    = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	wecomDeptURL     = "https://qyapi.weixin.qq.com/cgi-bin/department/list"
	wecomUsersURL    = "https://qyapi.weixin.qq.com/cgi-bin/user/simplelist"

	// feishuMembersBaseURL redirects the Feishu SDK at a test server; empty
	// means the official open.feishu.cn endpoints.
	feishuMembersBaseURL = ""
	membersHTTPClient    = &http.Client{Timeout: 5 * time.Second}
)

// memberBudget bounds a single fetch chain.
type memberBudget struct {
	requests int
}

func (b *memberBudget) spend() error {
	b.requests++
	if b.requests > maxMemberRequests {
		return &keeperError{Code: "invalid_response"}
	}
	return nil
}

// collectMembers appends deduped rows and sorts by (name, id) at the end.
type collectMembers struct {
	seen  map[string]string
	rows  []notificationMember
	dirty bool
}

func newCollectMembers() *collectMembers {
	return &collectMembers{seen: map[string]string{}}
}

func (c *collectMembers) add(id, name string) {
	id, name = trimMemberField(id), trimMemberField(name)
	if id == "" {
		return
	}
	if previous, ok := c.seen[id]; ok {
		if previous != name && name != "" && previous == "" {
			c.seen[id] = name
			c.dirty = true
		}
		return
	}
	if len(c.seen) >= maxMemberCount {
		return
	}
	c.seen[id] = name
	c.rows = append(c.rows, notificationMember{ID: id, Name: name})
}

func (c *collectMembers) sorted() []notificationMember {
	rows := make([]notificationMember, len(c.rows))
	copy(rows, c.rows)
	if c.dirty {
		for i := range rows {
			rows[i].Name = c.seen[rows[i].ID]
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func trimMemberField(v string) string {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
		v = v[1:]
	}
	for len(v) > 0 && (v[len(v)-1] == ' ' || v[len(v)-1] == '\t') {
		v = v[:len(v)-1]
	}
	return v
}

// membersStatusError maps an HTTP status to the closed code table (same
// semantics as keeperHTTPError, for callers holding a bare status int).
func membersStatusError(status int, retryAfter string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &keeperError{Code: "authentication_failed"}
	case http.StatusTooManyRequests:
		retry := 60 * time.Second
		if seconds, err := strconv.ParseInt(retryAfter, 10, 32); err == nil && seconds >= 0 {
			retry = time.Duration(seconds) * time.Second
		}
		return &keeperError{Code: "rate_limited", RetryAfter: retry}
	default:
		if status >= 500 {
			return &keeperError{Code: "connection_failed"}
		}
		return &keeperError{Code: "invalid_response"}
	}
}

// membersDo performs one JSON REST call for the hand-written platform
// clients: 200 → decode into out, anything else → closed error code.
func membersDo(ctx context.Context, method, rawURL string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &keeperError{Code: "invalid_response"}
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return &keeperError{Code: "configuration_error"}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := membersHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &keeperError{Code: "timeout"}
		}
		return &keeperError{Code: "connection_failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return membersStatusError(resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &keeperError{Code: "invalid_response"}
	}
	return nil
}

// ——— Feishu (official SDK) ———

func newFeishuMembersClient(appID, appSecret string) *lark.Client {
	options := []lark.ClientOptionFunc{
		lark.WithReqTimeout(5 * time.Second),
		lark.WithLogLevel(larkcore.LogLevelError),
	}
	if feishuMembersBaseURL != "" {
		options = append(options, lark.WithOpenBaseUrl(feishuMembersBaseURL))
	}
	return lark.NewClient(appID, appSecret, options...)
}

// feishuSDKError folds SDK-level failures into the closed code table: a
// token CodeError means the credentials were rejected, context expiry is a
// timeout, everything else is a transport failure. CodeError travels as a
// value type out of the SDK token manager, so match by value.
func feishuSDKError(ctx context.Context, err error) error {
	var codeError larkcore.CodeError
	if errors.As(err, &codeError) && codeError.Code != 0 {
		return &keeperError{Code: "authentication_failed"}
	}
	if ctx.Err() != nil {
		return &keeperError{Code: "timeout"}
	}
	return &keeperError{Code: "connection_failed"}
}

func fetchFeishuMembers(ctx context.Context, appID, appSecret string) ([]notificationMember, error) {
	client := newFeishuMembersClient(appID, appSecret)
	budget := &memberBudget{}
	collector := newCollectMembers()

	// Department tree: root (0) plus every descendant via fetch_child.
	deptIDs := []string{"0"}
	pageToken := ""
	for {
		if err := budget.spend(); err != nil {
			return nil, err
		}
		builder := larkcontact.NewChildrenDepartmentReqBuilder().
			DepartmentId("0").
			FetchChild(true).
			PageSize(50)
		if pageToken != "" {
			builder = builder.PageToken(pageToken)
		}
		resp, err := client.Contact.V3.Department.Children(ctx, builder.Build())
		if err != nil {
			return nil, feishuSDKError(ctx, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, membersStatusError(resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		if !resp.Success() {
			return nil, &keeperError{Code: "invalid_response"}
		}
		if resp.Data != nil {
			for _, dept := range resp.Data.Items {
				if dept.DepartmentId != nil {
					deptIDs = append(deptIDs, *dept.DepartmentId)
				}
			}
			if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
				break
			}
			pageToken = *resp.Data.PageToken
		} else {
			break
		}
	}

	for _, deptID := range deptIDs {
		token := ""
		for {
			if err := budget.spend(); err != nil {
				return nil, err
			}
			builder := larkcontact.NewFindByDepartmentUserReqBuilder().
				DepartmentId(deptID).
				UserIdType("open_id").
				PageSize(50)
			if token != "" {
				builder = builder.PageToken(token)
			}
			resp, err := client.Contact.V3.User.FindByDepartment(ctx, builder.Build())
			if err != nil {
				return nil, feishuSDKError(ctx, err)
			}
			if resp.StatusCode != http.StatusOK {
				return nil, membersStatusError(resp.StatusCode, resp.Header.Get("Retry-After"))
			}
			if !resp.Success() {
				return nil, &keeperError{Code: "invalid_response"}
			}
			if resp.Data == nil {
				break
			}
			for _, user := range resp.Data.Items {
				if user.OpenId != nil {
					name := ""
					if user.Name != nil {
						name = *user.Name
					}
					collector.add(*user.OpenId, name)
				}
			}
			if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
				break
			}
			token = *resp.Data.PageToken
		}
	}
	return collector.sorted(), nil
}

// ——— DingTalk (no official Go SDK; thin REST) ———

func fetchDingtalkMembers(ctx context.Context, appKey, appSecret string) ([]notificationMember, error) {
	var tokenResp struct {
		AccessToken string `json:"accessToken"`
	}
	if err := membersDo(ctx, http.MethodPost, dingtalkTokenURL, map[string]string{"appKey": appKey, "appSecret": appSecret}, &tokenResp); err != nil {
		// Any token-endpoint failure (bad credentials, malformed body) is an
		// authentication failure; transport/rate-limit codes pass through.
		var ke *keeperError
		if errors.As(err, &ke) && ke.Code == "invalid_response" {
			return nil, &keeperError{Code: "authentication_failed"}
		}
		return nil, err
	}
	if tokenResp.AccessToken == "" {
		return nil, &keeperError{Code: "authentication_failed"}
	}
	token := tokenResp.AccessToken

	budget := &memberBudget{}
	collector := newCollectMembers()
	type deptRow struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	// BFS from the root department (1); listsub returns direct children only.
	visited := map[int64]bool{}
	queue := []int64{1}
	var userDepts []int64
	for len(queue) > 0 {
		deptID := queue[0]
		queue = queue[1:]
		if visited[deptID] {
			continue
		}
		visited[deptID] = true
		userDepts = append(userDepts, deptID)
		if err := budget.spend(); err != nil {
			return nil, err
		}
		var deptResp struct {
			Errcode int      `json:"errcode"`
			Result  []deptRow `json:"result"`
		}
		url := fmt.Sprintf("%s?access_token=%s", dingtalkDeptURL, token)
		if err := membersDo(ctx, http.MethodPost, url, map[string]any{"dept_id": deptID}, &deptResp); err != nil {
			return nil, err
		}
		if deptResp.Errcode != 0 {
			return nil, &keeperError{Code: "invalid_response"}
		}
		for _, row := range deptResp.Result {
			if !visited[row.ID] {
				queue = append(queue, row.ID)
			}
		}
	}

	for _, deptID := range userDepts {
		cursor := int64(0)
		for {
			if err := budget.spend(); err != nil {
				return nil, err
			}
			var userResp struct {
				Errcode int `json:"errcode"`
				Result struct {
					HasMore    bool `json:"has_more"`
					NextCursor int64 `json:"next_cursor"`
					List []struct {
						UserID string `json:"userid"`
						Name   string `json:"name"`
					} `json:"list"`
				} `json:"result"`
			}
			url := fmt.Sprintf("%s?access_token=%s", dingtalkUsersURL, token)
			if err := membersDo(ctx, http.MethodPost, url, map[string]any{"dept_id": deptID, "cursor": cursor, "size": 100}, &userResp); err != nil {
				return nil, err
			}
			if userResp.Errcode != 0 {
				return nil, &keeperError{Code: "invalid_response"}
			}
			for _, row := range userResp.Result.List {
				collector.add(row.UserID, row.Name)
			}
			if !userResp.Result.HasMore {
				break
			}
			cursor = userResp.Result.NextCursor
		}
	}
	return collector.sorted(), nil
}

// ——— WeCom (no official Go SDK; thin REST) ———

func wecomBusinessError(errcode int) error {
	// 45009/45011 are WeCom's api-freq limit codes.
	if errcode == 45009 || errcode == 45011 {
		return &keeperError{Code: "rate_limited", RetryAfter: 60 * time.Second}
	}
	return &keeperError{Code: "invalid_response"}
}

func fetchWecomMembers(ctx context.Context, corpID, secret string) ([]notificationMember, error) {
	var tokenResp struct {
		Errcode     int    `json:"errcode"`
		AccessToken string `json:"access_token"`
	}
	if err := membersDo(ctx, http.MethodGet, fmt.Sprintf("%s?corpid=%s&corpsecret=%s", wecomTokenURL, corpID, secret), nil, &tokenResp); err != nil {
		return nil, err
	}
	if tokenResp.Errcode != 0 {
		// Bad credentials surface as an auth failure; rate codes pass through.
		if err := wecomBusinessError(tokenResp.Errcode); err != nil {
			var ke *keeperError
			if errors.As(err, &ke) && ke.Code == "rate_limited" {
				return nil, err
			}
		}
		return nil, &keeperError{Code: "authentication_failed"}
	}
	if tokenResp.AccessToken == "" {
		return nil, &keeperError{Code: "authentication_failed"}
	}
	token := tokenResp.AccessToken

	if err := (&memberBudget{}).spend(); err != nil {
		return nil, err
	}
	var deptResp struct {
		Errcode int `json:"errcode"`
		Department []struct {
			ID       int64 `json:"id"`
			ParentID int64 `json:"parentid"`
		} `json:"department"`
	}
	if err := membersDo(ctx, http.MethodGet, fmt.Sprintf("%s?access_token=%s", wecomDeptURL, token), nil, &deptResp); err != nil {
		return nil, err
	}
	if deptResp.Errcode != 0 {
		return nil, wecomBusinessError(deptResp.Errcode)
	}
	// Roots: the root department (1) plus any department whose parent is
	// absent from the listing (parentid 0 or an unknown id) — each root's
	// whole subtree is then walked once via fetch_child=1.
	ids := map[int64]bool{}
	for _, dept := range deptResp.Department {
		ids[dept.ID] = true
	}
	tops := map[int64]bool{1: true}
	for _, dept := range deptResp.Department {
		if dept.ParentID == 0 || !ids[dept.ParentID] {
			tops[dept.ID] = true
		}
	}
	budget := &memberBudget{}
	collector := newCollectMembers()
	for top := range tops {
		if err := budget.spend(); err != nil {
			return nil, err
		}
		var userResp struct {
			Errcode  int `json:"errcode"`
			UserList []struct {
				UserID string `json:"userid"`
				Name   string `json:"name"`
			} `json:"userlist"`
		}
		url := fmt.Sprintf("%s?access_token=%s&department_id=%d&fetch_child=1", wecomUsersURL, token, top)
		if err := membersDo(ctx, http.MethodGet, url, nil, &userResp); err != nil {
			return nil, err
		}
		if userResp.Errcode != 0 {
			return nil, wecomBusinessError(userResp.Errcode)
		}
		for _, row := range userResp.UserList {
			collector.add(row.UserID, row.Name)
		}
	}
	return collector.sorted(), nil
}

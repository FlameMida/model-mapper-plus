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
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
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

// memberBudget bounds a single fetch chain; safe for concurrent spenders
// (feishu walks departments with bounded parallelism).
type memberBudget struct {
	mu       sync.Mutex
	requests int
}

func (b *memberBudget) spend() error {
	b.mu.Lock()
	defer b.mu.Unlock()
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
// value type out of the SDK token manager, so match by value. The raw
// platform code/msg goes to the log only — never into the response envelope.
func feishuSDKError(ctx context.Context, err error) error {
	var codeError larkcore.CodeError
	if errors.As(err, &codeError) && codeError.Code != 0 {
		logger.Warn("feishu member fetch rejected by platform", "code", codeError.Code, "msg", codeError.Msg)
		return &keeperError{Code: "authentication_failed"}
	}
	if ctx.Err() != nil {
		return &keeperError{Code: "timeout"}
	}
	return &keeperError{Code: "connection_failed"}
}

func fetchFeishuMembers(ctx context.Context, appID, appSecret string) ([]notificationMember, error) {
	client := newFeishuMembersClient(appID, appSecret)

	// Explicit token preflight. A marketplace/ISV app behind the self-built
	// token endpoint answers code:0 with an EMPTY token, which the SDK
	// happily caches; every directory call then 401s with a blank bearer and
	// the failure reads as a confusing credential rejection. Reject it here
	// with a precise log line instead.
	tokenResp, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx,
		&larkcore.SelfBuiltTenantAccessTokenReq{AppID: appID, AppSecret: appSecret})
	if err != nil {
		return nil, feishuSDKError(ctx, err)
	}
	if tokenResp.Code != 0 {
		logger.Warn("feishu member fetch token rejected", "code", tokenResp.Code, "msg", tokenResp.Msg)
		return nil, &keeperError{Code: "authentication_failed"}
	}
	if tokenResp.TenantAccessToken == "" {
		logger.Warn("feishu returned an empty tenant token — the app is likely a marketplace (ISV) app; the member picker needs a self-built (企业自建) app")
		return nil, &keeperError{Code: "authentication_failed"}
	}

	budget := &memberBudget{}
	collector := newCollectMembers()

	// Directory-call status failures log the HTTP status: a silent 403
	// (permissions not granted or the version carrying them unpublished)
	// is the top real-world failure and must be diagnosable from the log.
	feishuStatusError := func(status int, retryAfter string) error {
		logger.Warn("feishu member fetch directory call rejected", "status", status)
		return membersStatusError(status, retryAfter)
	}

	// Authorized scope drives traversal (partial scopes work — walking root
	// children requires an all-member scope and fails with code 40004).
	// Scopes returns the selected departments, not their descendants, and
	// FindByDepartment is direct-only, so each authorized department is
	// expanded via FetchChild. Individually authorized users come from
	// user_ids and never appear in the department walk.
	deptIDs, userIDs, err := fetchFeishuAuthorizedScope(ctx, client, budget, feishuStatusError)
	if err != nil {
		return nil, err
	}
	seenDepts := map[string]bool{}
	var allDepts []string
	addDept := func(id string) {
		id = trimMemberField(id)
		if id == "" || seenDepts[id] {
			return
		}
		seenDepts[id] = true
		allDepts = append(allDepts, id)
	}
	for _, id := range deptIDs {
		addDept(id)
	}
	authorized := append([]string{}, allDepts...)
	for _, id := range authorized {
		children, err := fetchFeishuChildDepartmentIDs(ctx, client, budget, id, feishuStatusError)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			addDept(child)
		}
	}

	// Bounded-concurrency member fetch: the directory APIs allow 50 req/s,
	// serial per-department calls would brush the 30s budget on orgs with
	// hundreds of departments.
	type deptMembers struct {
		members []notificationMember
		err     error
	}
	results := make([]deptMembers, len(allDepts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, deptID := range allDepts {
		if err := budget.spend(); err != nil {
			results[i] = deptMembers{err: err}
			continue
		}
		wg.Add(1)
		go func(i int, deptID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			members, err := fetchFeishuDepartmentMembers(ctx, client, budget, deptID, feishuStatusError)
			results[i] = deptMembers{members: members, err: err}
		}(i, deptID)
	}
	wg.Wait()
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		for _, m := range result.members {
			collector.add(m.ID, m.Name)
		}
	}
	for _, id := range userIDs {
		collector.add(id, "")
	}
	if err := fillFeishuMissingNames(ctx, client, collector, budget, feishuStatusError); err != nil {
		logger.Warn("feishu member name batch skipped", "err", err)
	}
	return collector.sorted(), nil
}

func fillFeishuMissingNames(ctx context.Context, client *lark.Client, collector *collectMembers, budget *memberBudget, statusError func(int, string) error) error {
	var missing []string
	for _, row := range collector.rows {
		if strings.TrimSpace(row.Name) == "" && strings.TrimSpace(row.ID) != "" {
			missing = append(missing, row.ID)
		}
	}
	for i := 0; i < len(missing); i += 50 {
		end := i + 50
		if end > len(missing) {
			end = len(missing)
		}
		if err := budget.spend(); err != nil {
			return err
		}
		req := larkcontact.NewBatchUserReqBuilder().UserIds(missing[i:end]).UserIdType("open_id").Build()
		resp, err := client.Contact.V3.User.Batch(ctx, req)
		if err != nil {
			return nil
		}
		if resp.StatusCode != http.StatusOK || !resp.Success() || resp.Data == nil {
			return nil
		}
		for _, user := range resp.Data.Items {
			if user == nil || user.OpenId == nil {
				continue
			}
			name := ""
			if user.Name != nil {
				name = *user.Name
			}
			collector.add(*user.OpenId, name)
		}
	}
	return nil
}

func fetchFeishuAuthorizedScope(ctx context.Context, client *lark.Client, budget *memberBudget, statusError func(int, string) error) (deptIDs, userIDs []string, err error) {
	pageToken := ""
	for {
		if err := budget.spend(); err != nil {
			return nil, nil, err
		}
		builder := larkcontact.NewListScopeReqBuilder().
			UserIdType("open_id").
			DepartmentIdType("open_department_id").
			PageSize(50)
		if pageToken != "" {
			builder = builder.PageToken(pageToken)
		}
		resp, err := client.Contact.V3.Scope.List(ctx, builder.Build())
		if err != nil {
			return nil, nil, feishuSDKError(ctx, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, nil, statusError(resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		if !resp.Success() {
			logger.Warn("feishu member fetch scope rejected", "code", resp.Code, "msg", resp.Msg)
			return nil, nil, &keeperError{Code: "invalid_response"}
		}
		if resp.Data == nil {
			return deptIDs, userIDs, nil
		}
		deptIDs = append(deptIDs, resp.Data.DepartmentIds...)
		userIDs = append(userIDs, resp.Data.UserIds...)
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return deptIDs, userIDs, nil
		}
		pageToken = *resp.Data.PageToken
	}
}

func fetchFeishuChildDepartmentIDs(ctx context.Context, client *lark.Client, budget *memberBudget, deptID string, statusError func(int, string) error) ([]string, error) {
	var ids []string
	pageToken := ""
	for {
		if err := budget.spend(); err != nil {
			return nil, err
		}
		builder := larkcontact.NewChildrenDepartmentReqBuilder().
			DepartmentId(deptID).
			DepartmentIdType("open_department_id").
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
			return nil, statusError(resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		if !resp.Success() {
			logger.Warn("feishu member fetch children rejected", "code", resp.Code, "msg", resp.Msg, "department_id", deptID)
			return nil, &keeperError{Code: "invalid_response"}
		}
		if resp.Data == nil {
			return ids, nil
		}
		for _, dept := range resp.Data.Items {
			if id := feishuDepartmentOpenID(dept); id != "" {
				ids = append(ids, id)
			}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return ids, nil
		}
		pageToken = *resp.Data.PageToken
	}
}

func feishuDepartmentOpenID(dept *larkcontact.Department) string {
	if dept == nil {
		return ""
	}
	if dept.OpenDepartmentId != nil {
		if id := trimMemberField(*dept.OpenDepartmentId); id != "" {
			return id
		}
	}
	if dept.DepartmentId != nil {
		return trimMemberField(*dept.DepartmentId)
	}
	return ""
}

// fetchFeishuDepartmentMembers pages through one department's direct users.
func fetchFeishuDepartmentMembers(ctx context.Context, client *lark.Client, budget *memberBudget, deptID string, statusError func(int, string) error) ([]notificationMember, error) {
	var members []notificationMember
	token := ""
	for {
		if err := budget.spend(); err != nil {
			return nil, err
		}
		builder := larkcontact.NewFindByDepartmentUserReqBuilder().
			DepartmentId(deptID).
			DepartmentIdType("open_department_id").
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
			return nil, statusError(resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		if !resp.Success() {
			return nil, &keeperError{Code: "invalid_response"}
		}
		if resp.Data == nil {
			return members, nil
		}
		for _, user := range resp.Data.Items {
			if user.OpenId != nil {
				name := ""
				if user.Name != nil {
					name = *user.Name
				}
				members = append(members, notificationMember{ID: *user.OpenId, Name: name})
			}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return members, nil
		}
		token = *resp.Data.PageToken
	}
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
			logger.Warn("dingtalk member fetch token rejected", "status_hint", "non-200 token endpoint")
			return nil, &keeperError{Code: "authentication_failed"}
		}
		return nil, err
	}
	if tokenResp.AccessToken == "" {
		logger.Warn("dingtalk member fetch token rejected", "hint", "empty accessToken")
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
			Errcode int       `json:"errcode"`
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
				Result  struct {
					HasMore    bool  `json:"has_more"`
					NextCursor int64 `json:"next_cursor"`
					List       []struct {
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
	// Raw errcode goes to the log only; the response keeps the closed code.
	// 60020 (untrusted IP) is the top real-world failure: calling server APIs
	// requires the egress IP in the app's "企业可信IP" list.
	logger.Warn("wecom member fetch business error", "errcode", errcode)
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
		logger.Warn("wecom member fetch token rejected", "errcode", tokenResp.Errcode)
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
		Errcode    int `json:"errcode"`
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

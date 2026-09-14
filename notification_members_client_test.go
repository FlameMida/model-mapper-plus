// notification_members_client_test.go covers the three platform directory
// clients: request shape, pagination, department recursion, dedup and the
// closed error-code table. Upstream platforms are simulated with httptest
// servers behind swappable package-level endpoint URLs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pointFeishuMembersAt(base string) func() {
	old := feishuMembersBaseURL
	feishuMembersBaseURL = base
	return func() { feishuMembersBaseURL = old }
}

func pointDingtalkMembersAt(base string) func() {
	old := [3]string{dingtalkTokenURL, dingtalkDeptURL, dingtalkUsersURL}
	dingtalkTokenURL, dingtalkDeptURL, dingtalkUsersURL = base+"/dt/token", base+"/dt/dept", base+"/dt/users"
	return func() { dingtalkTokenURL, dingtalkDeptURL, dingtalkUsersURL = old[0], old[1], old[2] }
}

func pointWecomMembersAt(base string) func() {
	old := [3]string{wecomTokenURL, wecomDeptURL, wecomUsersURL}
	wecomTokenURL, wecomDeptURL, wecomUsersURL = base+"/wx/token", base+"/wx/dept", base+"/wx/users"
	return func() { wecomTokenURL, wecomDeptURL, wecomUsersURL = old[0], old[1], old[2] }
}

func requireMembers(t *testing.T, got []notificationMember, want []notificationMember) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("members=%+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("members[%d]=%+v want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestFeishuMembersClientFetches(t *testing.T) {
	var tokenBody string
	var childrenCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			raw, _ := io.ReadAll(r.Body)
			tokenBody = string(raw)
			fmt.Fprintf(w, `{"code":0,"msg":"ok","tenant_access_token":"t-1","expire":7200}`)
		case "/open-apis/contact/v3/scopes":
			// The authorized scope drives traversal: partial scope works
			// without all-member permissions (children(0) would 40004).
			fmt.Fprintf(w, `{"code":0,"data":{"department_ids":["d1","d2"],"has_more":false}}`)
		case "/open-apis/contact/v3/departments/0/children":
			childrenCalled = true
			fmt.Fprintf(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
		case "/open-apis/contact/v3/departments/d1/children", "/open-apis/contact/v3/departments/d2/children":
			fmt.Fprintf(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
		case "/open-apis/contact/v3/users/find_by_department":
			q := r.URL.Query()
			if q.Get("user_id_type") != "open_id" {
				t.Errorf("users query=%v", q)
			}
			switch q.Get("department_id") {
			case "d1":
				fmt.Fprintf(w, `{"code":0,"data":{"items":[{"name":"Alice","open_id":"ou_a"},{"name":"Ghost","open_id":""}],"has_more":false}}`)
			case "d2":
				fmt.Fprintf(w, `{"code":0,"data":{"items":[{"name":"Bob","open_id":"ou_b"},{"name":"Alice","open_id":"ou_a"}],"has_more":false}}`)
			default:
				t.Errorf("unexpected department %s", q.Get("department_id"))
			}
		}
	}))
	defer srv.Close()
	t.Cleanup(pointFeishuMembersAt(srv.URL))

	got, err := fetchFeishuMembers(context.Background(), "cli_x", "app-sec")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tokenBody, "cli_x") || !strings.Contains(tokenBody, "app-sec") {
		t.Fatalf("token body=%s", tokenBody)
	}
	if childrenCalled {
		t.Fatal("must traverse the authorized scope, not root children (40004)")
	}
	// Empty IDs drop; cross-department duplicates merge; result sorts by (name, id).
	requireMembers(t, got, []notificationMember{{ID: "ou_a", Name: "Alice"}, {ID: "ou_b", Name: "Bob"}})
}

// Scopes returns the selected departments only (not descendants) and may
// also list individually authorized users. FindByDepartment is direct-only,
// so the client must walk children of each authorized department and keep
// scope user_ids — otherwise a parent-department or people-only scope
// fetches an empty picker.
func TestFeishuMembersClientFetchesNestedDepartmentsAndScopeUsers(t *testing.T) {
	var rootChildrenCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprintf(w, `{"code":0,"msg":"ok","tenant_access_token":"t-1","expire":7200}`)
		case r.URL.Path == "/open-apis/contact/v3/scopes":
			fmt.Fprintf(w, `{"code":0,"data":{"department_ids":["d1"],"user_ids":["ou_solo"],"has_more":false}}`)
		case r.URL.Path == "/open-apis/contact/v3/departments/0/children":
			rootChildrenCalled = true
			fmt.Fprintf(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
		case r.URL.Path == "/open-apis/contact/v3/departments/d1/children":
			if r.URL.Query().Get("fetch_child") != "true" {
				t.Errorf("d1 children fetch_child=%v", r.URL.Query())
			}
			fmt.Fprintf(w, `{"code":0,"data":{"items":[{"open_department_id":"d1c","department_id":"leaf"}],"has_more":false}}`)
		case r.URL.Path == "/open-apis/contact/v3/users/find_by_department":
			switch r.URL.Query().Get("department_id") {
			case "d1":
				fmt.Fprintf(w, `{"code":0,"data":{"items":[{"name":"Alice","open_id":"ou_a"}],"has_more":false}}`)
			case "d1c":
				fmt.Fprintf(w, `{"code":0,"data":{"items":[{"name":"Carol","open_id":"ou_c"}],"has_more":false}}`)
			default:
				t.Errorf("unexpected department %s", r.URL.Query().Get("department_id"))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Cleanup(pointFeishuMembersAt(srv.URL))

	got, err := fetchFeishuMembers(context.Background(), "cli_x", "app-sec")
	if err != nil {
		t.Fatal(err)
	}
	if rootChildrenCalled {
		t.Fatal("must not walk root children (40004 on partial scope)")
	}
	requireMembers(t, got, []notificationMember{
		{ID: "ou_solo", Name: ""},
		{ID: "ou_a", Name: "Alice"},
		{ID: "ou_c", Name: "Carol"},
	})
}

func TestFeishuMembersClientErrors(t *testing.T) {
	for _, tc := range []struct {
		name, dept, users, code string
		deptStatus              int
	}{
		{"dept business code", `{"code":230002,"msg":"no permission"}`, "", "invalid_response", 0},
		{"dept http 401", "", "", "authentication_failed", http.StatusUnauthorized},
		{"dept http 429", "", "", "rate_limited", http.StatusTooManyRequests},
		{"dept http 500", "", "", "connection_failed", http.StatusInternalServerError},
		{"users business code", `{"code":0,"data":{"department_ids":["d1"],"has_more":false}}`, `{"code":230002}`, "invalid_response", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usersDone bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					fmt.Fprintf(w, `{"code":0,"tenant_access_token":"t-1","expire":7200}`)
				case "/open-apis/contact/v3/scopes":
					if tc.deptStatus != 0 {
						if tc.deptStatus == http.StatusTooManyRequests {
							w.Header().Set("Retry-After", "30")
						}
						w.WriteHeader(tc.deptStatus)
						// JSON error body keeps the SDK on the ApiResp path.
						fmt.Fprintf(w, `{"code":%d,"msg":"err"}`, tc.deptStatus)
						return
					}
					fmt.Fprint(w, tc.dept)
				case "/open-apis/contact/v3/departments/d1/children":
					fmt.Fprintf(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
				case "/open-apis/contact/v3/users/find_by_department":
					usersDone = true
					fmt.Fprint(w, tc.users)
				}
			}))
			defer srv.Close()
			t.Cleanup(pointFeishuMembersAt(srv.URL))
			_, err := fetchFeishuMembers(context.Background(), "cli_x", "app-sec")
			requireKeeperError(t, err, tc.code)
			if usersDone && tc.code != "invalid_response" {
				t.Fatal("must not call user API after dept failure")
			}
		})
	}
}

func TestFeishuMembersClientTokenRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// SDK token manager surfaces code!=0 as a CodeError error.
		fmt.Fprintf(w, `{"code":99991663,"msg":"app secret invalid"}`)
	}))
	defer srv.Close()
	t.Cleanup(pointFeishuMembersAt(srv.URL))
	_, err := fetchFeishuMembers(context.Background(), "cli_x", "wrong")
	requireKeeperError(t, err, "authentication_failed")
}

// A marketplace/ISV app behind the self-built token endpoint answers
// code:0 with an EMPTY token; the preflight must reject it up front without
// touching the directory APIs (which would 401 with a blank bearer).
func TestFeishuMembersClientEmptyTokenPreflight(t *testing.T) {
	var directoryCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			fmt.Fprintf(w, `{"code":0,"msg":"ok","tenant_access_token":"","expire":6103}`)
			return
		}
		directoryCalled = true
		fmt.Fprintf(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
	}))
	defer srv.Close()
	t.Cleanup(pointFeishuMembersAt(srv.URL))
	_, err := fetchFeishuMembers(context.Background(), "cli_x", "sec")
	requireKeeperError(t, err, "authentication_failed")
	if directoryCalled {
		t.Fatal("empty token must be rejected before any directory call")
	}
}

func TestDingtalkMembersClientFetches(t *testing.T) {
	var tokenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dt/token":
			raw, _ := io.ReadAll(r.Body)
			tokenBody = string(raw)
			fmt.Fprintf(w, `{"expireIn":7200,"accessToken":"t-1"}`)
		case "/dt/dept":
			if r.URL.Query().Get("access_token") != "t-1" {
				t.Errorf("dept access_token=%v", r.URL.Query())
			}
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				DeptID int `json:"dept_id"`
			}
			_ = json.Unmarshal(raw, &body)
			switch body.DeptID {
			case 1:
				fmt.Fprintf(w, `{"errcode":0,"result":[{"id":11,"name":"sub11"},{"id":12,"name":"sub12"}]}`)
			case 11:
				fmt.Fprintf(w, `{"errcode":0,"result":[{"id":111,"name":"leaf"}]}`)
			case 12, 111:
				fmt.Fprintf(w, `{"errcode":0,"result":[]}`)
			}
		case "/dt/users":
			if r.URL.Query().Get("access_token") != "t-1" {
				t.Errorf("users access_token=%v", r.URL.Query())
			}
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				DeptID int `json:"dept_id"`
				Cursor int `json:"cursor"`
			}
			_ = json.Unmarshal(raw, &body)
			switch {
			case body.DeptID == 1 && body.Cursor == 0:
				fmt.Fprintf(w, `{"errcode":0,"result":{"has_more":true,"next_cursor":5,"list":[{"userid":"u1","name":"Root"}]}}`)
			case body.DeptID == 1 && body.Cursor == 5:
				fmt.Fprintf(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"u2","name":"Page2"}]}}`)
			case body.DeptID == 11:
				fmt.Fprintf(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"u3","name":"Sub11"}]}}`)
			default:
				fmt.Fprintf(w, `{"errcode":0,"result":{"has_more":false,"list":[]}}`)
			}
		}
	}))
	defer srv.Close()
	t.Cleanup(pointDingtalkMembersAt(srv.URL))

	got, err := fetchDingtalkMembers(context.Background(), "ak-1", "sk-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tokenBody, `"appKey":"ak-1"`) || !strings.Contains(tokenBody, `"appSecret":"sk-1"`) {
		t.Fatalf("token body=%s", tokenBody)
	}
	// BFS over 1→{11,12}, 11→111; users from 1 (two pages) and 11, sorted by name.
	requireMembers(t, got, []notificationMember{
		{ID: "u2", Name: "Page2"}, {ID: "u1", Name: "Root"}, {ID: "u3", Name: "Sub11"},
	})
}

func TestDingtalkMembersClientErrors(t *testing.T) {
	for _, tc := range []struct {
		name, token, dept, users, code string
		tokenStatus                    int
	}{
		{"token non-200 bad secret", "", "", "", "authentication_failed", http.StatusBadRequest},
		{"token 429", "", "", "", "rate_limited", http.StatusTooManyRequests},
		{"token 5xx", "", "", "", "connection_failed", http.StatusInternalServerError},
		{"dept errcode", `{"accessToken":"t"}`, `{"errcode":60003,"errmsg":"no scope"}`, "", "invalid_response", 0},
		{"users errcode", `{"accessToken":"t"}`, `{"errcode":0,"result":[]}`, `{"errcode":60121}`, "invalid_response", 0},
		{"users malformed", `{"accessToken":"t"}`, `{"errcode":0,"result":[]}`, `{`, "invalid_response", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/dt/token":
					if tc.tokenStatus != 0 {
						w.WriteHeader(tc.tokenStatus)
						return
					}
					fmt.Fprint(w, tc.token)
				case "/dt/dept":
					fmt.Fprint(w, tc.dept)
				case "/dt/users":
					fmt.Fprint(w, tc.users)
				}
			}))
			defer srv.Close()
			t.Cleanup(pointDingtalkMembersAt(srv.URL))
			_, err := fetchDingtalkMembers(context.Background(), "ak", "sk")
			requireKeeperError(t, err, tc.code)
		})
	}
}

func TestWecomMembersClientFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wx/token":
			q := r.URL.Query()
			if q.Get("corpid") != "corp-1" || q.Get("corpsecret") != "sec-1" {
				t.Errorf("token query=%v", q)
			}
			fmt.Fprintf(w, `{"errcode":0,"errmsg":"ok","access_token":"t-1","expires_in":7200}`)
		case "/wx/dept":
			if r.URL.Query().Get("access_token") != "t-1" {
				t.Errorf("dept access_token=%v", r.URL.Query())
			}
			fmt.Fprintf(w, `{"errcode":0,"department":[
				{"id":1,"name":"root","parentid":0},
				{"id":2,"name":"tree2","parentid":1},
				{"id":10,"name":"tree10","parentid":99}]}`)
		case "/wx/users":
			q := r.URL.Query()
			if q.Get("fetch_child") != "1" {
				t.Errorf("users fetch_child=%v", q)
			}
			switch q.Get("department_id") {
			case "1":
				fmt.Fprintf(w, `{"errcode":0,"userlist":[{"userid":"zhang","name":"Zhang"},{"userid":"li","name":"Li"}]}`)
			case "10":
				fmt.Fprintf(w, `{"errcode":0,"userlist":[{"userid":"zhang","name":"Zhang"}]}`)
			default:
				t.Errorf("unexpected top department %s", q.Get("department_id"))
			}
		}
	}))
	defer srv.Close()
	t.Cleanup(pointWecomMembersAt(srv.URL))

	got, err := fetchWecomMembers(context.Background(), "corp-1", "sec-1")
	if err != nil {
		t.Fatal(err)
	}
	// Department 2 nests under 1 (covered by fetch_child=1); tree 10 repeats
	// zhang — dedup keeps the first name. Sorted by name.
	requireMembers(t, got, []notificationMember{{ID: "li", Name: "Li"}, {ID: "zhang", Name: "Zhang"}})
}

func TestWecomMembersClientErrors(t *testing.T) {
	for _, tc := range []struct {
		name, token, dept, users, code string
	}{
		{"token errcode", `{"errcode":40001,"errmsg":"bad secret"}`, "", "", "authentication_failed"},
		{"token rate limited code", `{"errcode":45009,"errmsg":"api freq out of limit"}`, "", "", "rate_limited"},
		{"dept errcode", `{"errcode":0,"access_token":"t"}`, `{"errcode":60011,"errmsg":"no privilege"}`, "", "invalid_response"},
		{"users errcode", `{"errcode":0,"access_token":"t"}`, `{"errcode":0,"department":[]}`, `{"errcode":60111}`, "invalid_response"},
		{"users malformed", `{"errcode":0,"access_token":"t"}`, `{"errcode":0,"department":[]}`, `nope`, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/wx/token":
					fmt.Fprint(w, tc.token)
				case "/wx/dept":
					fmt.Fprint(w, tc.dept)
				case "/wx/users":
					fmt.Fprint(w, tc.users)
				}
			}))
			defer srv.Close()
			t.Cleanup(pointWecomMembersAt(srv.URL))
			_, err := fetchWecomMembers(context.Background(), "corp", "sec")
			ke := requireKeeperError(t, err, tc.code)
			if tc.code == "rate_limited" && ke.RetryAfter != 60*time.Second {
				t.Fatalf("retry after=%v want 60s", ke.RetryAfter)
			}
		})
	}
}

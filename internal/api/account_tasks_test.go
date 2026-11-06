package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

type accountTaskFailure string

func (err accountTaskFailure) Error() string     { return "credential-secret" }
func (err accountTaskFailure) ErrorCode() string { return string(err) }

func TestCookieAccountTaskFailureIsolation(t *testing.T) {
	admin := &cookieImportAdmin{lifecycle: t.Context()}
	admin.importCookie = func(_ context.Context, input CookieImportInput) (AdminAccount, error) {
		if _, err := aistudio.ParseCookieImport(input.Cookies); err != nil {
			return AdminAccount{}, accountTaskFailure("invalid_account")
		}
		if input.AuthUser == 1 {
			return AdminAccount{}, accountTaskFailure("cookie_import_http_failed")
		}
		if input.AuthUser == 2 {
			return AdminAccount{}, accountTaskFailure("account_exists")
		}
		return AdminAccount{ID: "verified@example.test", Label: "verified@example.test"}, nil
	}
	handler := NewHandler(nil, Config{Admin: admin})
	const cookies = "SAPISID=credential-secret; __Secure-1PAPISID=one; __Secure-3PAPISID=three"
	input := cookieImportRequest{Items: []cookieImportItem{
		{Cookies: "invalid-cookie-text", Method: "http"},
		{Cookies: strings.Repeat("A=fixture;", 1001), Method: "http"},
		{Cookies: cookies, Method: "http", AuthUser: 1},
		{Cookies: cookies, AuthUser: 2},
		{Cookies: cookies, Email: "forged@example.test", AuthUser: 3},
	}}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := accountTaskRequest(handler, http.MethodPost, "/api/accounts/import/cookies", string(body), nil)
	var result struct{ Task AccountTask }
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("item-level errors must be tasks: %d %s", response.Code, response.Body)
	}
	task := waitAccountTask(t, handler, result.Task.ID, nil)
	if task.State != "failed" || len(task.Items) != len(input.Items) || len(admin.capturedInputs()) != len(input.Items) {
		t.Fatalf("batch stopped after one failure: %+v", task)
	}
	for index, code := range []string{"invalid_account", "invalid_account", "cookie_import_http_failed", "account_exists", ""} {
		if task.Items[index].Code != code || code != "" && task.Items[index].State != "failed" {
			t.Fatalf("item %d = %+v", index, task.Items[index])
		}
	}
	if task.Items[4].State != "done" || task.Items[4].Label != "verified@example.test" {
		t.Fatal("verified final item did not complete")
	}
	encoded, _ := json.Marshal(task)
	if strings.Contains(string(encoded), "credential-secret") || strings.Contains(string(encoded), "forged@example.test") {
		t.Fatal("task exposed credentials or unverified identity")
	}
}

func TestAccountTaskCancellationWaitsForRelease(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "lifecycle", true: "delete"}[remove], func(t *testing.T) {
			lifecycle, stop := context.WithCancel(t.Context())
			defer stop()
			requestCtx, disconnect := context.WithCancel(t.Context())
			defer disconnect()
			request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(requestCtx)
			var tasks accountTasks
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { close(release) })
			calls := 0
			task := tasks.start(request, lifecycle, AccountTask{Items: []AccountTaskItem{{State: "pending"}, {State: "pending"}, {State: "pending"}}}, func(ctx context.Context, update accountTaskUpdate) {
				runAccountTaskItems(ctx, update, 3, func(index int) (AdminAccount, error) {
					calls++
					if index == 0 {
						return AdminAccount{Label: "finished@example.test"}, nil
					}
					close(entered)
					<-ctx.Done()
					close(canceled)
					<-release
					return AdminAccount{}, ctx.Err()
				})
			})
			tasks.mu.Lock()
			entry := tasks.tasks[0]
			tasks.mu.Unlock()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("task did not start")
			}
			disconnect()
			select {
			case <-canceled:
				t.Fatal("HTTP disconnect canceled the background task")
			case <-time.After(10 * time.Millisecond):
			}
			removed := make(chan error, 1)
			if remove {
				go func() {
					found, err := tasks.remove(t.Context(), task.ID)
					if !found {
						err = errors.New("task disappeared before DELETE")
					}
					removed <- err
				}()
			} else {
				stop()
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("task did not receive cancellation")
			}
			if len(tasks.list()) != 1 {
				t.Error("DELETE removed the task before resource release")
			}
			select {
			case <-removed:
				t.Fatal("DELETE returned before resource release")
			default:
			}
			release <- struct{}{}
			select {
			case <-entry.done:
			case <-time.After(time.Second):
				t.Fatal("canceled task did not release resources")
			}
			if calls != 2 {
				t.Fatal("task ran an item after cancellation")
			}
			if remove {
				if err := <-removed; err != nil || len(tasks.list()) != 0 {
					t.Fatalf("DELETE did not remove the released task: %v", err)
				}
			} else {
				final := tasks.list()[0]
				if final.State != "failed" || final.Items[0].State != "done" {
					t.Fatalf("shutdown lost completed item: %+v", final)
				}
				for _, item := range final.Items[1:] {
					if item.State != "failed" || item.Code != "account_task_canceled" {
						t.Fatalf("shutdown did not retain canceled item: %+v", item)
					}
				}
			}
		})
	}
}

func TestAccountTaskCanceledSuccessIsNotDone(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	task := AccountTask{Items: []AccountTaskItem{{State: "pending"}}}
	runAccountTaskItems(ctx, func(update func(*AccountTask)) { update(&task) }, 1, func(int) (AdminAccount, error) {
		cancel()
		return AdminAccount{Label: "uncommitted@example.test"}, nil
	})
	if item := task.Items[0]; item.State != "failed" || item.Code != "account_task_canceled" || item.Label != "" {
		t.Fatalf("canceled operation reported success: %+v", item)
	}
}

type taskEndpointAdmin struct{ cookieImportAdmin }

func (*taskEndpointAdmin) CreateAccount(context.Context, AccountCreateInput, *camoufoxnative.LiveView) (AdminAccount, error) {
	return AdminAccount{Label: "created@example.test"}, nil
}

func (*taskEndpointAdmin) LoginAccount(context.Context, string, *camoufoxnative.LiveView) (AdminAccount, error) {
	return AdminAccount{Label: "relogin@example.test"}, nil
}

func (*taskEndpointAdmin) ImportChromeAccounts(context.Context, ChromeImportInput) ([]AdminAccount, error) {
	return []AdminAccount{{Label: "chrome@example.test"}}, nil
}

func (*taskEndpointAdmin) Events(context.Context) (<-chan AdminEvent, error) {
	return make(chan AdminEvent), nil
}

func TestAccountTaskEndpointsAndSSE(t *testing.T) {
	admin := &taskEndpointAdmin{cookieImportAdmin: cookieImportAdmin{lifecycle: t.Context()}}
	handler := NewHandler(nil, Config{Admin: admin})
	for _, test := range []struct{ path, body, kind string }{
		{"/api/accounts", `{}`, "login"},
		{"/api/accounts/relogin@example.test/login", `{}`, "relogin"},
		{"/api/accounts/import/chrome", `{"accounts":[{"id":"fixture-id","email":"forged@example.test"}]}`, "chrome"},
		{"/api/accounts/import/cookies", `{"items":[{"cookies":"fixture","method":"http"}]}`, "cookies"},
	} {
		response := accountTaskRequest(handler, http.MethodPost, test.path, test.body, nil)
		var result struct{ Task AccountTask }
		if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Task.Kind != test.kind {
			t.Fatalf("%s = %d %s", test.path, response.Code, response.Body)
		}
		waitAccountTask(t, handler, result.Task.ID, nil)
	}
	fixture := httptest.NewServer(handler)
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fixture.URL+"/api/events", nil)
	response, err := fixture.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Data struct {
				Tasks []AccountTask `json:"tasks"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "account_tasks" || len(event.Data.Tasks) != 4 {
			t.Fatalf("SSE did not restore in-memory tasks: %+v", event)
		}
		for _, task := range event.Data.Tasks {
			deleted := accountTaskRequest(handler, http.MethodDelete, "/api/accounts/tasks/"+task.ID, "", nil)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", deleted.Code)
			}
		}
		if missing := accountTaskRequest(handler, http.MethodDelete, "/api/accounts/tasks/missing", "", nil); missing.Code != http.StatusNotFound {
			t.Fatal("missing task DELETE did not return 404")
		}
		fresh := accountTaskRequest(NewHandler(nil, Config{Admin: admin}), http.MethodGet, "/api/accounts/tasks", "", nil)
		if strings.TrimSpace(fresh.Body.String()) != `{"tasks":[]}` {
			t.Fatalf("new handler recovered process-local tasks: %s", fresh.Body)
		}
		return
	}
	t.Fatalf("missing account_tasks event: %v", scanner.Err())
}

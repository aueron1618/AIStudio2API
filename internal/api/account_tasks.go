package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const maxAccountTaskItems = 100

func decodeAccountTaskInput(r *http.Request, input any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("账户任务请求必须是单个 JSON 对象")
	}
	return nil
}

// AccountTask 是在服务端运行的添加、重新登录或刷新账户任务，管理页关闭或刷新后继续运行，结束后保留到用户关闭
type AccountTask struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	View      string            `json:"view,omitempty"`
	State     string            `json:"state"`
	Items     []AccountTaskItem `json:"items"`
	StartedAt time.Time         `json:"started_at"`
}

// AccountTaskItem 是任务中的一个账户，Label 为邮箱，浏览器登录完成前为空；Step 是处理中的账户当前的步骤
type AccountTaskItem struct {
	Label string `json:"label"`
	State string `json:"state"`
	Step  string `json:"step,omitempty"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// 账户任务中一个账户的步骤：等待在浏览器中登录、读取浏览器里的登录状态、检查 AI Studio 访问、保存账户、核对验证状态、等待在验证页完成验证
const (
	TaskStepLogin     = "login"
	TaskStepReading   = "reading"
	TaskStepChecking  = "checking"
	TaskStepSaving    = "saving"
	TaskStepVerifying = "verifying"
	TaskStepVerify    = "verify"
)

// taskStepContextKey 是 context 中账户任务步骤回调的键
type taskStepContextKey struct{}

// taskLabelContextKey 是 context 中账户任务正在处理的账户标签的键
type taskLabelContextKey struct{}

// taskLabelReportContextKey 是 context 中账户任务更新账户标签回调的键
type taskLabelReportContextKey struct{}

// TaskLabel 返回 ctx 所属账户任务正在处理的账户标签，ctx 不属于账户任务时为空串
func TaskLabel(ctx context.Context) string {
	label, _ := ctx.Value(taskLabelContextKey{}).(string)
	return label
}

// ReportTaskStep 把账户进入的步骤显示到它所在的账户任务，ctx 不属于账户任务时不做任何事
func ReportTaskStep(ctx context.Context, step string) {
	if report, ok := ctx.Value(taskStepContextKey{}).(func(string)); ok {
		report(step)
	}
}

// ReportTaskLabel 把已知的账户邮箱写入进行中的账户任务，ctx 不属于账户任务时不做任何事
func ReportTaskLabel(ctx context.Context, label string) {
	if report, ok := ctx.Value(taskLabelReportContextKey{}).(func(string)); ok {
		report(label)
	}
}

// chromeImportRequest 是从本机 Chrome 导入的账号与共用的账户环境
type chromeImportRequest struct {
	Accounts []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"accounts"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// cookieImportItem 是 Cookie 导入中的一个账户：识别出的邮箱、Cookie 文本或浏览器调试地址与会话序号
type cookieImportItem struct {
	Email    string `json:"email"`
	Cookies  string `json:"cookies"`
	CDP      string `json:"cdp"`
	AuthUser int    `json:"auth_user"`
	Method   string `json:"method,omitempty"`
}

// cookieImportRequest 是一次导入的 Cookie 账户与共用的账户环境，语言与时区为空时按出口位置填写
type cookieImportRequest struct {
	Items    []cookieImportItem `json:"items"`
	Proxy    string             `json:"proxy"`
	Locale   string             `json:"locale"`
	Timezone string             `json:"timezone"`
}

// accountTasks 保存账户任务，任务列表变化时推送给管理事件流
type accountTasks struct {
	mu          sync.Mutex
	tasks       []*accountTaskEntry
	subscribers map[chan []AccountTask]struct{}
}

// accountTaskEntry 是登记中的任务与它的取消函数
type accountTaskEntry struct {
	task   AccountTask
	cancel context.CancelFunc
	done   chan struct{}
}

// accountTaskUpdate 在锁内修改任务进度并推送任务列表，任务已被关闭时不修改
type accountTaskUpdate func(func(*AccountTask))

// clone 返回不与登记中的任务共用账户列表的副本
func (task AccountTask) clone() AccountTask {
	task.Items = slices.Clone(task.Items)
	return task
}

// start 登记任务并在不随请求取消的 context 中运行 run，run 返回后有账户失败时任务为 failed，否则为 done
func (tasks *accountTasks) start(r *http.Request, lifecycle context.Context, task AccountTask, run func(context.Context, accountTaskUpdate)) AccountTask {
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	stop := context.AfterFunc(lifecycle, cancel)
	if lifecycle.Err() != nil {
		cancel()
	}
	task.ID = rand.Text()
	task.State = "running"
	task.StartedAt = time.Now().UTC()
	entry := &accountTaskEntry{task: task.clone(), cancel: cancel, done: make(chan struct{})}
	tasks.mu.Lock()
	tasks.tasks = append(tasks.tasks, entry)
	tasks.publishLocked()
	tasks.mu.Unlock()
	update := func(change func(*AccountTask)) {
		tasks.mu.Lock()
		defer tasks.mu.Unlock()
		if slices.Contains(tasks.tasks, entry) {
			change(&entry.task)
			tasks.publishLocked()
		}
	}
	go func() {
		defer close(entry.done)
		defer stop()
		defer cancel()
		run(ctx, update)
		update(func(task *AccountTask) {
			task.State = "done"
			if slices.ContainsFunc(task.Items, func(item AccountTaskItem) bool { return item.State != "done" }) {
				task.State = "failed"
			}
		})
	}()
	return task
}

// list 按开始顺序返回全部任务
func (tasks *accountTasks) list() []AccountTask {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return tasks.snapshotLocked()
}

// remove 取消进行中的任务或关闭已结束的任务，任务不存在时返回 false
func (tasks *accountTasks) remove(ctx context.Context, id string) (bool, error) {
	tasks.mu.Lock()
	index := slices.IndexFunc(tasks.tasks, func(entry *accountTaskEntry) bool { return entry.task.ID == id })
	if index < 0 {
		tasks.mu.Unlock()
		return false, nil
	}
	entry := tasks.tasks[index]
	entry.cancel()
	tasks.mu.Unlock()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return true, ctx.Err()
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if index := slices.Index(tasks.tasks, entry); index >= 0 {
		tasks.tasks = slices.Delete(tasks.tasks, index, index+1)
		tasks.publishLocked()
	}
	return true, nil
}

// subscribe 返回任务列表的变化，先送出当前列表，读取不及时时只保留最新列表
func (tasks *accountTasks) subscribe(ctx context.Context) <-chan []AccountTask {
	updates := make(chan []AccountTask, 1)
	tasks.mu.Lock()
	if tasks.subscribers == nil {
		tasks.subscribers = make(map[chan []AccountTask]struct{})
	}
	tasks.subscribers[updates] = struct{}{}
	updates <- tasks.snapshotLocked()
	tasks.mu.Unlock()
	context.AfterFunc(ctx, func() {
		tasks.mu.Lock()
		delete(tasks.subscribers, updates)
		tasks.mu.Unlock()
	})
	return updates
}

// snapshotLocked 返回任务列表的副本
func (tasks *accountTasks) snapshotLocked() []AccountTask {
	list := make([]AccountTask, len(tasks.tasks))
	for index, entry := range tasks.tasks {
		list[index] = entry.task.clone()
	}
	return list
}

// publishLocked 把最新任务列表放入每个订阅，替换尚未读取的旧列表
func (tasks *accountTasks) publishLocked() {
	list := tasks.snapshotLocked()
	for updates := range tasks.subscribers {
		select {
		case <-updates:
		default:
		}
		updates <- list
	}
}

// runAccountTaskItems 依次处理任务中的各个账户，单个账户失败时记录原因后继续下一个，任务被取消后停止
// Each item receives its own progress callbacks while retaining lifecycle cancellation.
func runAccountTaskItems(ctx context.Context, update accountTaskUpdate, count int, run func(context.Context, int) (AdminAccount, error)) {
	for index := range count {
		if ctx.Err() != nil {
			update(func(task *AccountTask) {
				item := &task.Items[index]
				item.State, item.Code, item.Error, item.Step = "failed", "account_task_canceled", "账户任务已取消", ""
			})
			continue
		}
		var label string
		update(func(task *AccountTask) {
			task.Items[index].State = "running"
			label = task.Items[index].Label
		})
		itemCtx := context.WithValue(ctx, taskLabelContextKey{}, label)
		itemCtx = context.WithValue(itemCtx, taskLabelReportContextKey{}, func(nextLabel string) {
			if nextLabel != "" {
				update(func(task *AccountTask) { task.Items[index].Label = nextLabel })
			}
		})
		itemCtx = context.WithValue(itemCtx, taskStepContextKey{}, func(step string) {
			update(func(task *AccountTask) { task.Items[index].Step = step })
		})
		account, err := run(itemCtx, index)
		if err == nil {
			err = ctx.Err()
		}
		update(func(task *AccountTask) {
			item := &task.Items[index]
			item.Step = ""
			if err != nil {
				item.State, item.Code, item.Error = "failed", codeFromError(err, "account_task_failed"), "账户操作失败，未能完成该项"
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
					item.Code, item.Error = "account_task_canceled", "账户任务已取消或超时"
				}
				return
			}
			item.State, item.Label = "done", account.Label
		})
	}
}

// startAccountTask 登记并启动账户任务，返回 202 与任务
func (s *server) startAccountTask(w http.ResponseWriter, r *http.Request, task AccountTask, run func(context.Context, accountTaskUpdate)) bool {
	lifecycle := s.config.Admin.AccountTaskContext()
	if lifecycle.Err() != nil {
		writeAdminError(w, http.StatusServiceUnavailable, "account_task_canceled", "账户任务已取消")
		return false
	}
	writeJSON(w, http.StatusAccepted, map[string]AccountTask{"task": s.accountTasks.start(r, lifecycle, task, run)})
	return true
}

// handleAccountTasks 返回账户任务
func (s *server) handleAccountTasks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]AccountTask{"tasks": s.accountTasks.list()})
}

// handleDeleteAccountTask 取消进行中的账户任务或关闭已结束的任务
func (s *server) handleDeleteAccountTask(w http.ResponseWriter, r *http.Request) {
	found, err := s.accountTasks.remove(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	if !found {
		writeAdminError(w, http.StatusNotFound, "task_not_found", "Account task not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateAccount 在账户任务中以浏览器登录添加账户，登录显示在管理页登录窗口时任务带登录窗口 ID
func (s *server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AccountCreateInput
		View string `json:"view"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
	if err := decodeAccountTaskInput(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "账户任务请求必须是有效且不超限的 JSON")
		return
	}
	view, release := s.loginView(r, input.View)
	task := AccountTask{Kind: "login", Items: []AccountTaskItem{{State: "pending"}}}
	if view != nil {
		task.View = input.View
	}
	if !s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		defer release()
		runAccountTaskItems(ctx, update, 1, func(ctx context.Context, _ int) (AdminAccount, error) {
			return s.config.Admin.CreateAccount(ctx, input.AccountCreateInput, view)
		})
	}) {
		release()
	}
}

// handleLoginAccount 在账户任务中打开浏览器重新登录账户
func (s *server) handleLoginAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		View string `json:"view"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
		if err := decodeAccountTaskInput(r, &input); err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "账户任务请求必须是有效且不超限的 JSON")
			return
		}
	}
	id := r.PathValue("id")
	view, release := s.loginView(r, input.View)
	task := AccountTask{Kind: "relogin", Items: []AccountTaskItem{{Label: id, State: "pending"}}}
	if view != nil {
		task.View = input.View
	}
	if !s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		defer release()
		runAccountTaskItems(ctx, update, 1, func(ctx context.Context, _ int) (AdminAccount, error) {
			return s.config.Admin.LoginAccount(ctx, id, view)
		})
	}) {
		release()
	}
}

// handleVerifyAccount 在账户任务中打开浏览器停在验证页供用户完成验证，验证后自动核对
func (s *server) handleVerifyAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		View string `json:"view"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
		if err := decodeAccountTaskInput(r, &input); err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "账户任务请求必须是有效且不超限的 JSON")
			return
		}
	}
	id := r.PathValue("id")
	view, release := s.loginView(r, input.View)
	task := AccountTask{Kind: "verify", Items: []AccountTaskItem{{Label: id, State: "pending"}}}
	if view != nil {
		task.View = input.View
	}
	if !s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		defer release()
		runAccountTaskItems(ctx, update, 1, func(ctx context.Context, _ int) (AdminAccount, error) {
			return s.config.Admin.VerifyAccount(ctx, id, view)
		})
	}) {
		release()
	}
}

// refreshTaskConcurrency 是刷新任务同时刷新的账户数
const refreshTaskConcurrency = 4

// handleRefreshAccounts 在账户任务中按 refreshTaskConcurrency 的并发依次刷新所列账户，账户标签为账户 ID，任务被关闭后不再开始刷新
func (s *server) handleRefreshAccounts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
	if err := decodeAccountTaskInput(r, &input); err != nil || len(input.IDs) == 0 || len(input.IDs) > maxAccountTaskItems {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "账户任务请求必须是有效且不超限的 JSON")
		return
	}
	task := AccountTask{Kind: "refresh", Items: make([]AccountTaskItem, len(input.IDs))}
	next := make(chan int, len(input.IDs))
	for index, id := range input.IDs {
		if strings.TrimSpace(id) == "" {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "账户任务请求必须是有效且不超限的 JSON")
			return
		}
		task.Items[index] = AccountTaskItem{Label: id, State: "pending"}
		next <- index
	}
	close(next)
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		var group sync.WaitGroup
		for range min(refreshTaskConcurrency, len(input.IDs)) {
			group.Go(func() {
				for index := range next {
					if ctx.Err() != nil {
						update(func(task *AccountTask) {
							item := &task.Items[index]
							item.State, item.Code, item.Error = "failed", "account_task_canceled", "账户任务已取消"
						})
						continue
					}
					update(func(task *AccountTask) { task.Items[index].State = "running" })
					_, err := s.config.Admin.RefreshAccount(ctx, input.IDs[index])
					if err == nil {
						err = ctx.Err()
					}
					update(func(task *AccountTask) {
						item := &task.Items[index]
						if err != nil {
							item.State, item.Code, item.Error = "failed", codeFromError(err, "account_task_failed"), "账户操作失败，未能完成该项"
							if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
								item.Code, item.Error = "account_task_canceled", "账户任务已取消或超时"
							}
							return
						}
						item.State = "done"
					})
				}
			})
		}
		group.Wait()
	})
}

// handleImportChromeAccounts imports selected Chrome accounts with per-item results.
func (s *server) handleImportChromeAccounts(w http.ResponseWriter, r *http.Request) {
	var input chromeImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
	if err := decodeAccountTaskInput(r, &input); err != nil || len(input.Accounts) == 0 || len(input.Accounts) > maxAccountTaskItems {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "需要有效的 Chrome 账号列表，一次最多 100 项")
		return
	}
	task := AccountTask{Kind: "chrome", Items: make([]AccountTaskItem, len(input.Accounts))}
	for index, account := range input.Accounts {
		if strings.TrimSpace(account.ID) == "" {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "Chrome 账号 ID 不能为空")
			return
		}
		task.Items[index] = AccountTaskItem{State: "pending"}
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		runAccountTaskItems(ctx, update, len(input.Accounts), func(ctx context.Context, index int) (AdminAccount, error) {
			accounts, err := s.config.Admin.ImportChromeAccounts(ctx, ChromeImportInput{
				AccountIDs: []string{input.Accounts[index].ID},
				Proxy:      input.Proxy, Locale: input.Locale, Timezone: input.Timezone,
			})
			if err != nil {
				return AdminAccount{}, err
			}
			if len(accounts) != 1 {
				return AdminAccount{}, errors.New("Chrome 账号导入未返回单个账户")
			}
			return accounts[0], nil
		})
	})
}

// handleImportCookieAccounts imports Cookie accounts with per-item results.
func (s *server) handleImportCookieAccounts(w http.ResponseWriter, r *http.Request) {
	var input cookieImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxCookieImportRequestBytes)
	if err := decodeAccountTaskInput(r, &input); err != nil || len(input.Items) == 0 || len(input.Items) > maxAccountTaskItems {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", "需要有效的 Cookie 账号列表，一次最多 100 项")
		return
	}
	task := AccountTask{Kind: "cookies", Items: make([]AccountTaskItem, len(input.Items))}
	for index := range input.Items {
		item := &input.Items[index]
		item.Method = strings.ToLower(strings.TrimSpace(item.Method))
		if item.Method == "" {
			item.Method = "browser"
		}
		if len(item.Cookies) > aistudio.MaxCookieImportBytes || strings.TrimSpace(item.Cookies) == "" && strings.TrimSpace(item.CDP) == "" {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "需要 Cookie 文本或浏览器调试地址，Cookie 文本不能超过 1 MiB")
			return
		}
		if item.Method != "browser" && item.Method != "http" || item.AuthUser < 0 || item.AuthUser > 9 {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", "Cookie 验证方式必须为 browser 或 http，auth_user 必须在 0..9 之间")
			return
		}
		task.Items[index] = AccountTaskItem{State: "pending"}
		if strings.TrimSpace(item.CDP) != "" {
			task.Kind = "browser"
		}
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		runAccountTaskItems(ctx, update, len(input.Items), func(ctx context.Context, index int) (AdminAccount, error) {
			return s.importCookieAccount(ctx, input.Items[index], input.Proxy, input.Locale, input.Timezone)
		})
	})
}

// importCookieAccount 读取调试浏览器的 Cookie 或解析 Cookie 文本，以识别会话时得到的邮箱为标签导入第 auth_user 个会话登录的账户
func (s *server) importCookieAccount(ctx context.Context, item cookieImportItem, proxy string, locale string, timezone string) (AdminAccount, error) {
	return s.config.Admin.ImportCookieAccount(ctx, CookieImportInput{
		Cookies: item.Cookies, CDP: item.CDP, Method: item.Method,
		Proxy: proxy, Locale: locale, Timezone: timezone, AuthUser: item.AuthUser,
	})
}

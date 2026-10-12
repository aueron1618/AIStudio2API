package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// ImportCookieAccount 先验证 Cookie 和实际邮箱，再发布账户；失败不留下账户目录。
func (admin *runtimeAdmin) ImportCookieAccount(ctx context.Context, input api.CookieImportInput) (api.AdminAccount, error) {
	state, err := aistudio.ParseCookieImport(input.Cookies)
	if err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	accountConfig := aistudio.DefaultAccountConfig("")
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	if locale := strings.TrimSpace(input.Locale); locale != "" {
		accountConfig.Locale = locale
	}
	if timezone := strings.TrimSpace(input.Timezone); timezone != "" {
		accountConfig.Timezone = timezone
	}
	if err := config.ValidateProxy(accountConfig.Proxy); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	importer, ok := admin.login.(aistudio.CookieLoginDriver)
	if !ok {
		return api.AdminAccount{}, &adminOperationError{
			status: http.StatusNotImplemented, code: "cookie_import_unavailable", message: "当前登录驱动不支持 Cookie 导入",
		}
	}
	directory, err := os.MkdirTemp("", "aistudio2api-cookie-import-*")
	if err != nil {
		return api.AdminAccount{}, fmt.Errorf("创建 Cookie 导入目录: %w", err)
	}
	defer os.RemoveAll(directory)
	admin.requests.log("auth", "INFO", "Cookie 导入 | 验证 AI Studio 登录状态")
	result, err := importer.ImportCookies(ctx, aistudio.IsolatedLoginRequest{
		AccountID: "new", Directory: directory, Proxy: admin.effectiveProxy(accountConfig.Proxy),
		Locale: accountConfig.Locale, Timezone: accountConfig.Timezone,
	}, state)
	if err != nil {
		// 浏览器协议错误可能包含注入的 Cookie 参数，不向日志或调用方回传原文。
		admin.requests.log("auth", "WARN", "Cookie 导入失败 | 未保存账户")
		switch {
		case errors.Is(err, camoufoxnative.ErrAuthenticationRequired):
			return api.AdminAccount{}, invalidAccount(fmt.Errorf("Cookie 登录已失效，请重新导出完整 Cookie"))
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusGatewayTimeout, code: "cookie_import_timeout", message: "Cookie 验证已取消或超时，请检查网络、代理和登录状态",
			}
		default:
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusBadGateway, code: "cookie_import_failed", message: "无法验证 Cookie 或识别邮箱，请检查 Camoufox、代理及 AI Studio 登录状态",
			}
		}
	}
	accountConfig.Label = strings.ToLower(strings.TrimSpace(result.Email))
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("无法识别有效的 AI Studio 登录邮箱"))
	}
	if err := result.StorageState.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("验证后的 Cookie 状态无效"))
	}
	if _, err := aistudio.NewSigner().Sign(result.StorageState); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("验证后的 Cookie 不完整或已过期"))
	}
	// 仅记录实际识别的身份，不保留导入文本可能伪造的扩展信息。
	if err := result.StorageState.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "cookie", Email: accountConfig.Label},
	}); err != nil {
		return api.AdminAccount{}, err
	}
	for _, status := range admin.pool.Status() {
		if strings.EqualFold(status.ID, accountConfig.Label) {
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusConflict, code: "account_exists", message: "账户已存在: " + status.ID,
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return api.AdminAccount{}, err
	}
	account, err := admin.addAccount(ctx, accountConfig, result.StorageState, directory)
	if err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log("auth", "INFO", "Cookie 账户已导入 | 账户="+account.Label)
	return account, nil
}

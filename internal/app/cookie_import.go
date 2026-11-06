package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// ImportCookieAccount 先验证 Cookie 和实际邮箱，再发布账户；失败不留下账户目录。
func (admin *runtimeAdmin) ImportCookieAccount(ctx context.Context, input api.CookieImportInput) (api.AdminAccount, error) {
	method := strings.ToLower(strings.TrimSpace(input.Method))
	if method == "" {
		method = "browser"
	}
	if method != "browser" && method != "http" {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("Cookie 验证方式必须为 browser 或 http"))
	}
	if input.AuthUser < 0 || input.AuthUser > 9 {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("账户 auth_user 无效: %d", input.AuthUser))
	}
	if len(input.Cookies) > aistudio.MaxCookieImportBytes {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("Cookie 文本不能超过 1 MiB"))
	}
	if address := strings.TrimSpace(input.CDP); address != "" {
		browser, err := chromeauth.ReadCDPBrowser(ctx, address)
		if err != nil {
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusBadGateway, code: "cdp_unavailable", message: "无法读取浏览器 Cookie，请检查远程调试地址及浏览器状态",
			}
		}
		input.Cookies = browser.Cookies
		if strings.TrimSpace(input.Locale) == "" {
			input.Locale = browser.Locale
		}
		if strings.TrimSpace(input.Timezone) == "" {
			input.Timezone = browser.Timezone
		}
	}
	state, err := aistudio.ParseCookieImport(input.Cookies)
	if err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	accountConfig := aistudio.DefaultAccountConfig("")
	accountConfig.AuthUser = input.AuthUser
	accountConfig.Proxy = strings.TrimSpace(input.Proxy)
	if locale := strings.TrimSpace(input.Locale); locale != "" {
		accountConfig.Locale = locale
	}
	timezone, err := accountTimezone(input.Timezone)
	if err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	if timezone != "" {
		accountConfig.Timezone = timezone
	}
	if err := config.ValidateProxy(accountConfig.Proxy); err != nil {
		return api.AdminAccount{}, invalidAccount(err)
	}
	var result aistudio.IsolatedLoginResult
	var directory string
	if method == "http" {
		admin.requests.log("auth", "INFO", "Cookie 导入 | 验证 AI Studio 登录状态 | method=http")
		var verification chromeauth.Verification
		verification, err = admin.verifyCookieState(ctx, &state, admin.effectiveProxy(accountConfig.Proxy), input.AuthUser)
		result = aistudio.IsolatedLoginResult{Email: verification.Email, StorageState: state}
	} else {
		importer, ok := admin.login.(aistudio.CookieLoginDriver)
		if !ok {
			return api.AdminAccount{}, &adminOperationError{
				status: http.StatusNotImplemented, code: "cookie_import_unavailable", message: "当前登录驱动不支持 Cookie 导入",
			}
		}
		directory, err = os.MkdirTemp("", "aistudio2api-cookie-import-*")
		if err != nil {
			return api.AdminAccount{}, fmt.Errorf("创建 Cookie 导入目录: %w", err)
		}
		defer os.RemoveAll(directory)
		admin.requests.log("auth", "INFO", "Cookie 导入 | 验证 AI Studio 登录状态 | method=browser")
		result, err = importer.ImportCookies(ctx, aistudio.IsolatedLoginRequest{
			Directory: directory, Proxy: admin.effectiveProxy(accountConfig.Proxy),
			Locale: accountConfig.Locale, Timezone: accountConfig.Timezone, AuthUser: input.AuthUser,
		}, state)
	}
	if err != nil {
		// 浏览器协议或 HTTP 错误可能包含凭据，不向日志或调用方回传原文。
		admin.requests.log("auth", "WARN", "Cookie 导入失败 | 未保存账户")
		return api.AdminAccount{}, cookieVerificationError(err, method)
	}
	if result.CloseError != "" {
		admin.requests.log("auth", "WARN", "Cookie 导入 | 关闭 Camoufox 失败 | 已验证的认证状态将继续保存")
	}
	accountConfig.Label = strings.ToLower(strings.TrimSpace(result.Email))
	if err := accountConfig.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("无法识别有效的 AI Studio 登录邮箱"))
	}
	if err := result.StorageState.Validate(); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("验证后的 Cookie 状态无效"))
	}
	if _, err := aistudio.NewSigner().Authorization(result.StorageState); err != nil {
		return api.AdminAccount{}, invalidAccount(fmt.Errorf("验证后的 Cookie 不完整或已过期"))
	}
	// 仅记录实际识别的身份，不保留导入文本可能伪造的扩展信息。
	result.StorageState.SetAuthExtension(aistudio.AuthExtension{
		Source: aistudio.AuthSource{Browser: "cookie", Email: accountConfig.Label},
	})
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
	account, err := admin.addAccount(ctx, accountConfig, result.StorageState, directory, "")
	if err != nil {
		return api.AdminAccount{}, err
	}
	if method == "browser" {
		admin.syncAccountModelCatalog(ctx, account.ID)
		account, err = admin.account(account.ID)
		if err != nil {
			return api.AdminAccount{}, err
		}
	} else {
		// PageCatalog may need a browser; HTTP import only commits verified authentication.
		admin.service.publishModelAccess()
	}
	admin.requests.log("auth", "INFO", "Cookie 账户已导入 | 账户="+account.Label)
	return account, nil
}

func (admin *runtimeAdmin) verifyCookieState(ctx context.Context, state *aistudio.StorageState, proxy string, authUser int) (chromeauth.Verification, error) {
	verify := admin.verifyCookies
	if verify == nil {
		verify = chromeauth.VerifyAuthUser
	}
	timeout := admin.config.InitTimeout
	if timeout <= 0 {
		timeout = config.Default().InitTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := verify(ctx, state, proxy, authUser)
	if err == nil {
		err = ctx.Err()
	}
	return result, err
}

func importCookieState(cookies []aistudio.StateCookie) (aistudio.StorageState, error) {
	data, err := json.Marshal(cookies)
	if err != nil {
		return aistudio.StorageState{}, fmt.Errorf("Cookie 格式无效")
	}
	return aistudio.ParseCookieImport(string(data))
}

func cookieVerificationError(err error, method string) error {
	switch {
	case errors.Is(err, camoufoxnative.ErrAuthenticationRequired), errors.Is(err, chromeauth.ErrSignedOut):
		return invalidAccount(fmt.Errorf("Cookie 登录已失效，请重新导出完整 Cookie"))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &adminOperationError{status: http.StatusGatewayTimeout, code: "cookie_import_timeout", message: "Cookie 验证已取消或超时，请检查网络、代理和登录状态"}
	default:
		if method == "http" {
			return &adminOperationError{status: http.StatusBadGateway, code: "cookie_import_http_failed", message: "无法通过 HTTP 验证 Cookie 或识别邮箱，请检查代理和 AI Studio 登录状态，或改用浏览器验证"}
		}
		return &adminOperationError{status: http.StatusBadGateway, code: "cookie_import_failed", message: "无法验证 Cookie 或识别邮箱，请检查 Camoufox、代理及 AI Studio 登录状态"}
	}
}

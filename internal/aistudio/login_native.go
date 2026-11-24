package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// loginTimeout 是隔离登录从启动浏览器到导出认证状态的期限
const loginTimeout = 30 * time.Minute

// NativeLoginDriver 通过纯 Go WebDriver BiDi 完成隔离登录
type NativeLoginDriver struct {
	camoufox string
}

var _ IsolatedLoginDriver = (*NativeLoginDriver)(nil)
var _ CookieLoginDriver = (*NativeLoginDriver)(nil)
var _ AccountVerifyDriver = (*NativeLoginDriver)(nil)

// NewNativeLoginDriver 创建纯 Go Camoufox 登录驱动
func NewNativeLoginDriver(camoufoxPath string) (*NativeLoginDriver, error) {
	absolute, err := filepath.Abs(camoufoxPath)
	if err != nil {
		return nil, fmt.Errorf("解析 Camoufox 路径: %w", err)
	}
	return &NativeLoginDriver{camoufox: absolute}, nil
}

// Login 启动隔离 Camoufox 供用户登录并导出认证状态
func (driver *NativeLoginDriver) Login(ctx context.Context, request IsolatedLoginRequest) (IsolatedLoginResult, error) {
	if driver == nil {
		return IsolatedLoginResult{}, errors.New("纯 Go Camoufox 登录驱动未初始化")
	}
	options := driver.options(request)
	options.OnVerifyStep = request.OnVerifyStep
	options.VerifyPollInterval = request.VerifyPollInterval
	if request.OnLoggedIn != nil {
		options.OnLoggedIn = func(callbackCtx context.Context, raw camoufoxnative.LoginResult) error {
			result, err := decodeNativeLoginResult(raw, "camoufox")
			if err != nil {
				return err
			}
			return request.OnLoggedIn(callbackCtx, result)
		}
	}
	if request.CheckVerification != nil {
		options.CheckVerification = func(callbackCtx context.Context, raw []byte) (bool, error) {
			var state StorageState
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &state); err != nil {
					return true, fmt.Errorf("解析验证状态: %w", err)
				}
				if err := state.Validate(); err != nil {
					return true, err
				}
			}
			return request.CheckVerification(callbackCtx, state)
		}
	}
	result, err := camoufoxnative.Login(ctx, options)
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	return decodeNativeLoginResult(result, "camoufox")
}

// ImportCookies 使用无头隔离 Camoufox 校验 Cookie 并识别实际账户。
func (driver *NativeLoginDriver) ImportCookies(ctx context.Context, request IsolatedLoginRequest, state StorageState) (IsolatedLoginResult, error) {
	if driver == nil {
		return IsolatedLoginResult{}, errors.New("纯 Go Camoufox 登录驱动未初始化")
	}
	if err := state.Validate(); err != nil {
		return IsolatedLoginResult{}, err
	}
	state = StorageState{Cookies: state.Cookies}
	encoded, err := json.Marshal(state)
	if err != nil {
		return IsolatedLoginResult{}, fmt.Errorf("编码 Cookie 导入状态: %w", err)
	}
	result, err := camoufoxnative.ImportCookies(ctx, driver.options(request), encoded)
	if err != nil {
		return IsolatedLoginResult{}, err
	}
	return decodeNativeLoginResult(result, "cookie")
}

func decodeNativeLoginResult(result camoufoxnative.LoginResult, source string) (IsolatedLoginResult, error) {
	var state StorageState
	if err := json.Unmarshal(result.StorageStateJSON, &state); err != nil {
		return IsolatedLoginResult{}, fmt.Errorf("解析隔离登录状态: %w", err)
	}
	if err := state.Validate(); err != nil {
		return IsolatedLoginResult{}, err
	}
	state.SetAuthExtension(AuthExtension{
		Source: AuthSource{Browser: source, Email: result.Email},
	})
	return IsolatedLoginResult{
		StorageState: state, Email: result.Email, VerifiedAt: result.VerifiedAt,
		DriveError: result.DriveError, CloseError: result.CloseError,
		Saved: result.Saved, VerificationPending: result.VerificationPending, VerificationError: result.VerificationError,
	}, nil
}

func (driver *NativeLoginDriver) options(request IsolatedLoginRequest) camoufoxnative.LoginOptions {
	return camoufoxnative.LoginOptions{
		ExecutablePath: driver.camoufox,
		Directory:      request.Directory,
		Locale:         request.Locale,
		Timezone:       request.Timezone,
		AuthUser:       request.AuthUser,
		Proxy:          request.Proxy,
		Timeout:        loginTimeout,
		View:           request.View,
	}
}

// Verify 启动隔离 Camoufox 载入已有账户的认证状态并停在官网验证页，自动再核对直到通过或窗口关闭
func (driver *NativeLoginDriver) Verify(ctx context.Context, request AccountVerifyRequest) (bool, error) {
	if driver == nil {
		return true, errors.New("纯 Go Camoufox 登录驱动未初始化")
	}
	if err := request.StorageState.Validate(); err != nil {
		return true, err
	}
	encoded, err := json.Marshal(request.StorageState)
	if err != nil {
		return true, fmt.Errorf("编码账户认证状态: %w", err)
	}
	options := camoufoxnative.VerifyOptions{
		ExecutablePath:   driver.camoufox,
		Directory:        request.Directory,
		StorageStateJSON: encoded,
		AuthUser:         request.AuthUser,
		Locale:           request.Locale,
		Timezone:         request.Timezone,
		Proxy:            request.Proxy,
		Timeout:          loginTimeout,
		View:             request.View,
		OnVerifyStep:     request.OnVerifyStep,
		PollInterval:     request.PollInterval,
	}
	if request.CheckVerification != nil {
		options.CheckVerification = func(callbackCtx context.Context, raw []byte) (bool, error) {
			var state StorageState
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &state); err != nil {
					return true, fmt.Errorf("解析验证状态: %w", err)
				}
				if err := state.Validate(); err != nil {
					return true, err
				}
			}
			return request.CheckVerification(callbackCtx, state)
		}
	}
	return camoufoxnative.Verify(ctx, options)
}

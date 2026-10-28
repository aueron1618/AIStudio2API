package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// NativeLoginDriver 通过纯 Go WebDriver BiDi 完成隔离登录
type NativeLoginDriver struct {
	camoufox string
	timeout  time.Duration
}

var _ IsolatedLoginDriver = (*NativeLoginDriver)(nil)
var _ CookieLoginDriver = (*NativeLoginDriver)(nil)

// NewNativeLoginDriver 创建纯 Go Camoufox 登录驱动
func NewNativeLoginDriver(camoufoxPath string, timeout time.Duration) (*NativeLoginDriver, error) {
	camoufoxPath = strings.TrimSpace(camoufoxPath)
	if camoufoxPath == "" {
		return nil, errors.New("缺少 Camoufox 路径")
	}
	absolute, err := filepath.Abs(camoufoxPath)
	if err != nil {
		return nil, fmt.Errorf("解析 Camoufox 路径: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("读取 Camoufox: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("Camoufox 路径是目录")
	}
	if timeout <= 0 {
		return nil, errors.New("Camoufox 登录超时必须为正数")
	}
	return &NativeLoginDriver{camoufox: absolute, timeout: timeout}, nil
}

// Login 启动隔离 Camoufox 供用户登录并导出认证状态
func (driver *NativeLoginDriver) Login(ctx context.Context, request IsolatedLoginRequest) (IsolatedLoginResult, error) {
	if driver == nil {
		return IsolatedLoginResult{}, errors.New("纯 Go Camoufox 登录驱动未初始化")
	}
	result, err := camoufoxnative.Login(ctx, driver.options(request))
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
	if err := state.SetAuthExtension(AuthExtension{
		Source: AuthSource{Browser: source, Email: result.Email},
	}); err != nil {
		return IsolatedLoginResult{}, err
	}
	return IsolatedLoginResult{
		StorageState: state, Email: result.Email, VerifiedAt: result.VerifiedAt,
		DriveError: result.DriveError, CloseError: result.CloseError,
	}, nil
}

func (driver *NativeLoginDriver) options(request IsolatedLoginRequest) camoufoxnative.LoginOptions {
	return camoufoxnative.LoginOptions{
		ExecutablePath: driver.camoufox,
		Directory:      request.Directory,
		Locale:         request.Locale,
		Timezone:       request.Timezone,
		Proxy:          request.Proxy,
		Timeout:        driver.timeout,
		View:           request.View,
	}
}

package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ImportCookies 在无头隔离浏览器中验证 Cookie，并读取实际登录邮箱和轮换后的状态。
// 不等待交互式登录或 Drive 授权，适用于没有桌面的服务器。
func ImportCookies(ctx context.Context, options LoginOptions, storageStateJSON []byte) (result LoginResult, err error) {
	options, err = validateLoginOptions(options)
	if err != nil {
		return LoginResult{}, err
	}
	var state storageState
	if json.Unmarshal(storageStateJSON, &state) != nil || len(state.Cookies) == 0 {
		return LoginResult{}, errors.New("Cookie 导入状态无效")
	}
	importCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	session, err := startLoginSession(importCtx, options, true, state)
	if err != nil {
		return LoginResult{}, err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	pageURL, authenticated, _, err := session.waitVerification(importCtx)
	if err != nil {
		return LoginResult{}, err
	}
	if !authenticated {
		return LoginResult{}, ErrAuthenticationRequired
	}
	email, err := session.client.evaluateString(importCtx, session.contextID, loginEmailExpression)
	if err != nil {
		return LoginResult{}, fmt.Errorf("读取 AI Studio 登录邮箱: %w", err)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return LoginResult{}, errors.New("AI Studio 页面没有登录邮箱")
	}
	origins := make(map[string]storageOrigin)
	session.captureCurrentOrigin(importCtx, pageURL, origins)
	state, err = session.exportStorageState(importCtx, origins)
	if err != nil {
		return LoginResult{}, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return LoginResult{}, fmt.Errorf("编码 Cookie 导入状态: %w", err)
	}
	return LoginResult{
		StorageStateJSON: encoded, Email: email, PageURL: pageURL, VerifiedAt: time.Now().UTC(),
	}, nil
}

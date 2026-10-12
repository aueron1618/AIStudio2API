package aistudio

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxCookieImportBytes 限制一次 Cookie 导入的原始文本大小。
const MaxCookieImportBytes = 1 << 20

const maxImportCookies = 1000

// ParseCookieImport 将请求头、Cookie 扩展 JSON 或 Netscape 文本转换为认证状态。
// 只导入 Google Cookie，不信任上传文件中的邮箱、OAuth 材料或 localStorage。
func ParseCookieImport(raw string) (StorageState, error) {
	if len(raw) > MaxCookieImportBytes {
		return StorageState{}, fmt.Errorf("Cookie 文本不能超过 1 MiB")
	}
	text := strings.Trim(strings.TrimPrefix(raw, "\ufeff"), " \r\n")
	if text == "" {
		return StorageState{}, fmt.Errorf("Cookie 文本不能为空")
	}
	var cookies []StateCookie
	var err error
	switch text[0] {
	case '[', '{':
		cookies, err = parseImportCookieJSON(text)
	default:
		cookies, err = parseImportCookieText(text)
	}
	if err != nil {
		return StorageState{}, err
	}
	if len(cookies) > maxImportCookies {
		return StorageState{}, fmt.Errorf("一次最多导入 %d 条 Cookie", maxImportCookies)
	}
	state := StorageState{}
	seen := make(map[string]int)
	now := float64(time.Now().Unix())
	for index, cookie := range cookies {
		cookie.Domain = strings.ToLower(strings.TrimSpace(cookie.Domain))
		if cookie.Domain == "" {
			cookie.Domain = importCookieDomain(cookie.Name)
		}
		host := strings.TrimPrefix(cookie.Domain, ".")
		if host != "google.com" && !strings.HasSuffix(host, ".google.com") {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		switch strings.ToLower(cookie.SameSite) {
		case "", "unspecified", "lax":
			cookie.SameSite = "Lax"
		case "none", "no_restriction":
			cookie.SameSite = "None"
		case "strict":
			cookie.SameSite = "Strict"
		default:
			return StorageState{}, fmt.Errorf("第 %d 条 Cookie 的 SameSite 无效", index+1)
		}
		if cookie.SameSite == "None" || strings.HasPrefix(cookie.Name, "__Secure-") || strings.HasPrefix(cookie.Name, "__Host-") {
			cookie.Secure = true
		}
		if strings.HasPrefix(cookie.Name, "__Host-") && (strings.HasPrefix(cookie.Domain, ".") || cookie.Path != "/") {
			return StorageState{}, fmt.Errorf("第 %d 条 Cookie 的 __Host- 域或路径无效", index+1)
		}
		candidate := http.Cookie{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path}
		if candidate.Valid() != nil || !strings.HasPrefix(cookie.Path, "/") || math.IsNaN(cookie.Expires) || math.IsInf(cookie.Expires, 0) {
			// 不返回底层错误，避免将 Cookie 值带入 API 错误或日志。
			return StorageState{}, fmt.Errorf("第 %d 条 Cookie 格式无效", index+1)
		}
		if cookie.PartitionKey != "" || cookie.Value == "" || cookie.Expires >= 0 && cookie.Expires <= now {
			continue
		}
		key := cookie.Domain + "\x00" + cookie.Path + "\x00" + cookie.Name
		if previous, ok := seen[key]; ok {
			if state.Cookies[previous].Value != cookie.Value {
				return StorageState{}, fmt.Errorf("第 %d 条 Cookie 与前面的同域同名 Cookie 冲突", index+1)
			}
			state.Cookies[previous] = cookie
			continue
		}
		seen[key] = len(state.Cookies)
		state.Cookies = append(state.Cookies, cookie)
	}
	if len(state.Cookies) == 0 {
		return StorageState{}, fmt.Errorf("没有可导入的有效 Google Cookie")
	}
	if _, err := NewSigner().Sign(state); err != nil {
		return StorageState{}, fmt.Errorf("Cookie 不完整或已过期，需要 SAPISID、__Secure-1PAPISID 和 __Secure-3PAPISID")
	}
	return state, nil
}

func parseImportCookieJSON(text string) ([]StateCookie, error) {
	raw := json.RawMessage(text)
	if text[0] == '{' {
		var envelope struct {
			Cookies json.RawMessage `json:"cookies"`
		}
		if json.Unmarshal(raw, &envelope) != nil || len(envelope.Cookies) == 0 {
			return nil, fmt.Errorf("Cookie JSON 对象需要 cookies 数组")
		}
		raw = envelope.Cookies
	}
	var items []struct {
		StateCookie
		Expires        *float64 `json:"expires"`
		ExpirationDate *float64 `json:"expirationDate"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("Cookie JSON 必须是 Cookie 对象数组或包含 cookies 数组的对象")
	}
	cookies := make([]StateCookie, 0, len(items))
	for _, item := range items {
		cookie := item.StateCookie
		cookie.Expires = -1
		if item.Expires != nil {
			cookie.Expires = *item.Expires
		} else if item.ExpirationDate != nil {
			cookie.Expires = *item.ExpirationDate
		}
		cookies = append(cookies, cookie)
	}
	return cookies, nil
}

func parseImportCookieText(text string) ([]StateCookie, error) {
	var cookies []StateCookie
	for lineIndex, line := range strings.Split(text, "\n") {
		line = strings.Trim(line, " \r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}
		if strings.Contains(line, "\t") {
			cookie, err := parseNetscapeCookie(line)
			if err != nil {
				return nil, fmt.Errorf("第 %d 行 Netscape Cookie 格式无效", lineIndex+1)
			}
			cookies = append(cookies, cookie)
			continue
		}
		if len(line) >= len("cookie:") && strings.EqualFold(line[:len("cookie:")], "cookie:") {
			line = strings.TrimSpace(line[len("cookie:"):])
		}
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, ok := strings.Cut(part, "=")
			if !ok {
				return nil, fmt.Errorf("第 %d 行不是 Cookie name=value 格式", lineIndex+1)
			}
			name, value = strings.TrimSpace(name), strings.TrimSpace(value)
			if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
			cookies = append(cookies, StateCookie{
				Name: name, Value: value, Domain: importCookieDomain(name), Path: "/", Expires: -1,
				Secure: true, HTTPOnly: !importScriptCookie(name), SameSite: "None",
			})
		}
	}
	return cookies, nil
}

func parseNetscapeCookie(line string) (StateCookie, error) {
	httpOnly := strings.HasPrefix(line, "#HttpOnly_")
	columns := strings.Split(strings.TrimPrefix(line, "#HttpOnly_"), "\t")
	if len(columns) != 7 {
		return StateCookie{}, fmt.Errorf("需要七列")
	}
	expires, err := strconv.ParseFloat(columns[4], 64)
	if err != nil {
		return StateCookie{}, err
	}
	if expires == 0 {
		expires = -1 // Netscape 用 0 表示 session Cookie。
	}
	if !strings.EqualFold(columns[1], "true") && !strings.EqualFold(columns[1], "false") ||
		!strings.EqualFold(columns[3], "true") && !strings.EqualFold(columns[3], "false") {
		return StateCookie{}, fmt.Errorf("无效布尔值")
	}
	domain := columns[0]
	if strings.EqualFold(columns[1], "true") && !strings.HasPrefix(domain, ".") {
		domain = "." + domain
	}
	return StateCookie{
		Name: columns[5], Value: columns[6], Domain: domain, Path: columns[2], Expires: expires,
		HTTPOnly: httpOnly, Secure: strings.EqualFold(columns[3], "true"), SameSite: "Lax",
	}, nil
}

// 请求头不含域信息，按 Google Cookie 的已知作用域补齐；JSON/Netscape 保留原域。
func importCookieDomain(name string) string {
	switch name {
	case "OSID", "__Secure-OSID", "__Secure-BUCKET":
		return "aistudio.google.com"
	case "OTZ", "LSID", "SMSV", "LSOLH", "ACCOUNT_CHOOSER", "__Host-GAPS", "__Host-1PLSID", "__Host-3PLSID":
		return "accounts.google.com"
	default:
		if strings.HasPrefix(name, "__Host-") {
			return "aistudio.google.com"
		}
		return ".google.com"
	}
}

func importScriptCookie(name string) bool {
	switch name {
	case "SID", "APISID", "SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID":
		return true
	default:
		return false
	}
}

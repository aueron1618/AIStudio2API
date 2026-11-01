package config

import (
	"path/filepath"
	"testing"
)

func TestAutoContinuationConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := Default()
	if !cfg.AutoContinuation {
		t.Fatal("existing automatic continuation must remain enabled by default")
	}
	for _, enabled := range []bool{false, true} {
		cfg.AutoContinuation = enabled
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.AutoContinuation != enabled {
			t.Fatalf("roundtrip enabled=%v: config=%+v error=%v", enabled, loaded, err)
		}
	}
	t.Setenv("AUTO_CONTINUATION", "false")
	loaded, err := Load(path)
	if err != nil || loaded.AutoContinuation {
		t.Fatalf("environment override was ignored: %+v, %v", loaded, err)
	}
	t.Setenv("AUTO_CONTINUATION", "invalid")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid auto-continuation setting was accepted")
	}
}

// TestValidateProxyCredentials 验证代理 URL 接受账号密码并拒绝空用户名
func TestValidateProxyCredentials(t *testing.T) {
	for _, value := range []string{"http://user:secret@127.0.0.1:8080", "socks5://user:secret@127.0.0.1:1080"} {
		if err := ValidateProxy(value); err != nil {
			t.Errorf("ValidateProxy(%q) = %v", value, err)
		}
	}
	if err := ValidateProxy("http://:secret@127.0.0.1:8080"); err == nil {
		t.Error("空用户名被接受")
	}
}

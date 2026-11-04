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
	loaded, err = Load(path)
	if err != nil || !loaded.AutoContinuation || len(loaded.Invalid) != 1 || loaded.Invalid[0].Key != "AUTO_CONTINUATION" {
		t.Fatalf("invalid auto-continuation must use the default and report the key: %+v, %v", loaded, err)
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

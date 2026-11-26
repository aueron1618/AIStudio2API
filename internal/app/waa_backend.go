package app

import (
	"context"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// camoufoxWorkerPath 是 Camoufox 后端 Worker 选项中的可执行文件路径，Worker 启动时换成已就绪的 Camoufox
const camoufoxWorkerPath = "camoufox"

// prepareWAABackend 按 WAA 后端返回 Worker 选项中的 Camoufox 路径与账户登录驱动，Camoufox 后端在后台准备 Camoufox
func prepareWAABackend(cfg config.Config, requests *requestRegistry, accounts []*aistudio.Account, proxy func() string) (string, aistudio.IsolatedLoginDriver) {
	removeStaleCamoufoxProfiles(requests)
	login := &lazyLoginDriver{proxy: proxy}
	if cfg.WAABackend == config.WAABackendSpiderMonkey {
		requests.logStage("service", "INFO", api.LogStage{Name: "运行时装配", Step: 2, Steps: 3}, "使用 SpiderMonkey WAA 后端", "accounts", len(accounts))
		for _, account := range accounts {
			if err := camoufoxnative.RemoveAccountCache(account.StoragePath); err != nil {
				requests.log(account.ID, "WARN", "Camoufox 缓存清理失败", "error", err)
			}
		}
		return "", login
	}
	requests.logStage("service", "INFO", api.LogStage{Name: "运行时装配", Step: 2, Steps: 3}, "使用 Camoufox WAA 后端", "accounts", len(accounts))
	camoufoxnative.PrepareInBackground(proxy())
	return camoufoxWorkerPath, login
}

// removeStaleCamoufoxProfiles 清理上次进程遗留的 Camoufox profile
func removeStaleCamoufoxProfiles(requests *requestRegistry) {
	if removed, err := camoufoxnative.RemoveStaleProfiles(); err != nil {
		requests.log("service", "WARN", "遗留 Camoufox profile 清理失败", "removed", removed, "error", err)
	} else if removed > 0 {
		requests.log("service", "INFO", "遗留 Camoufox profile 已清理", "removed", removed)
	}
}

// newWAAWorker 启动账户的 Camoufox 或 SpiderMonkey Worker，Camoufox 尚未就绪时返回准备进度或失败原因
func newWAAWorker(ctx context.Context, accountID string, options camoufoxnative.Options) (managedWorker, error) {
	var worker *aistudio.NativeWorker
	var err error
	if options.ExecutablePath == "" {
		worker, err = aistudio.NewSpiderMonkeyWorker(ctx, accountID, options)
	} else {
		options.ExecutablePath, err = camoufoxnative.ReadyPath()
		if err != nil {
			return nil, err
		}
		worker, err = aistudio.NewNativeWorker(ctx, accountID, options)
	}
	if err != nil {
		return nil, err
	}
	return worker, nil
}

// lazyLoginDriver 在显式登录或浏览器 Cookie 验证时共用进程内的 Camoufox 准备，不在 Go 后端启动时下载
type lazyLoginDriver struct {
	proxy func() string
}

func (lazy *lazyLoginDriver) resolve(ctx context.Context) (*aistudio.NativeLoginDriver, error) {
	proxy := ""
	if lazy.proxy != nil {
		proxy = lazy.proxy()
	}
	path, err := camoufoxnative.Prepare(ctx, proxy)
	if err != nil {
		return nil, err
	}
	return aistudio.NewNativeLoginDriver(path)
}

// Login 等 Camoufox 就绪后执行隔离登录
func (lazy *lazyLoginDriver) Login(ctx context.Context, request aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
	driver, err := lazy.resolve(ctx)
	if err != nil {
		return aistudio.IsolatedLoginResult{}, err
	}
	return driver.Login(ctx, request)
}

// ImportCookies 按需准备无头 Camoufox，验证导入 Cookie 并识别邮箱。
func (lazy *lazyLoginDriver) ImportCookies(ctx context.Context, request aistudio.IsolatedLoginRequest, state aistudio.StorageState) (aistudio.IsolatedLoginResult, error) {
	driver, err := lazy.resolve(ctx)
	if err != nil {
		return aistudio.IsolatedLoginResult{}, err
	}
	return driver.ImportCookies(ctx, request, state)
}

// Verify 等 Camoufox 就绪后打开账户验证页并自动再核对
func (lazy *lazyLoginDriver) Verify(ctx context.Context, request aistudio.AccountVerifyRequest) (bool, error) {
	login, err := lazy.resolve(ctx)
	if err != nil {
		return true, err
	}
	return login.Verify(ctx, request)
}

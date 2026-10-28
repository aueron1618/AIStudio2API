package waa

import (
	"runtime"
	"sync"
	"weak"

	"github.com/Mag1cFall/goja"
)

// objectStrings 为宿主对象保存不可枚举的标记，不延长对象或其 Realm 的生命周期。
// 值只允许字符串，不能经由值反向引用弱键。GC cleanup 与 VM 线程通过 mu 同步。
type objectStrings struct {
	mu     sync.Mutex
	values map[weak.Pointer[goja.Object]]string
}

func newObjectStrings() *objectStrings {
	return &objectStrings{values: make(map[weak.Pointer[goja.Object]]string)}
}

func (table *objectStrings) set(object *goja.Object, value string) {
	key := weak.Make(object)
	table.mu.Lock()
	_, exists := table.values[key]
	table.values[key] = value
	table.mu.Unlock()
	if !exists {
		// cleanup 也不能强引用 table，否则丢弃的整个注册表仍会常驻。
		owner := weak.Make(table)
		runtime.AddCleanup(object, func(key weak.Pointer[goja.Object]) {
			if table := owner.Value(); table != nil {
				table.mu.Lock()
				delete(table.values, key)
				table.mu.Unlock()
			}
		}, key)
	}
	runtime.KeepAlive(object)
}

func (table *objectStrings) get(object *goja.Object) (string, bool) {
	key := weak.Make(object)
	table.mu.Lock()
	value, ok := table.values[key]
	table.mu.Unlock()
	runtime.KeepAlive(object)
	return value, ok
}

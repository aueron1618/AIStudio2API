package waa

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"weak"

	"github.com/Mag1cFall/goja"
)

// 形状表只供宿主读取。共享紧凑的 Go 数据，不在每个账户中 JSON.parse 出
// 数万个完整 JS 对象。动态包装属于各自 VM，不共享可变 JS 原型或账户状态。
type shapeFields struct {
	keys   []string
	values map[string]any
}

type shapeData struct{ value any }

var shapeCache struct {
	sync.Mutex
	data weak.Pointer[shapeData]
}

func firefoxShape(vm *goja.Runtime, closureName string) (goja.Value, error) {
	shapeCache.Lock()
	defer shapeCache.Unlock()
	data := shapeCache.data.Value()
	if data == nil {
		source, err := firefoxShapeJSON()
		if err != nil {
			return nil, err
		}
		value, err := readShapeValue(json.NewDecoder(strings.NewReader(source)))
		if err != nil {
			return nil, err
		}
		data = &shapeData{value: value}
		shapeCache.data = weak.Make(data)
	}
	view := &shapeView{vm: vm, data: data, closureName: closureName}
	return view.wrap(data.value), nil
}

// 保留 JSON 对象的原始键顺序，Object.assign/entries 的结果不能因 Go map 而变化。
func readShapeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		fields := &shapeFields{values: make(map[string]any)}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("形状表对象键不是字符串")
			}
			value, err := readShapeValue(decoder)
			if err != nil {
				return nil, err
			}
			if _, exists := fields.values[name]; !exists {
				fields.keys = append(fields.keys, name)
			}
			fields.values[name] = value
		}
		_, err = decoder.Token()
		return fields, err
	case json.Delim('['):
		var values []any
		for decoder.More() {
			value, err := readShapeValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = decoder.Token()
		return values, err
	default:
		return token, nil
	}
}

// 随机监听表名属于 runtime 视图，不能写回弱缓存中的共享数据。
type shapeView struct {
	vm          *goja.Runtime
	data        *shapeData
	closureName string
}

func replaceClosureListenerMap(value, name string) string {
	if name == "" || !strings.Contains(value, "closure_lm_") {
		return value
	}
	return closureListenerMapPattern.ReplaceAllLiteralString(value, name)
}

func (view *shapeView) wrap(value any) goja.Value {
	switch value := value.(type) {
	case *shapeFields:
		return view.vm.NewDynamicObject(&shapeObject{view: view, fields: value})
	case []any:
		return view.vm.NewDynamicArray(&shapeArray{view: view, values: value})
	case string:
		return view.vm.ToValue(replaceClosureListenerMap(value, view.closureName))
	default:
		return view.vm.ToValue(value)
	}
}

type shapeObject struct {
	view   *shapeView
	fields *shapeFields
}

func (object *shapeObject) lookup(key string) (any, bool) {
	if object.view.closureName != "" && strings.Contains(key, "closure_lm_") {
		for _, original := range object.fields.keys {
			if replaceClosureListenerMap(original, object.view.closureName) == key {
				return object.fields.values[original], true
			}
		}
		return nil, false
	}
	value, ok := object.fields.values[key]
	return value, ok
}

func (object *shapeObject) Get(key string) goja.Value {
	value, ok := object.lookup(key)
	if !ok {
		return nil
	}
	return object.view.wrap(value)
}
func (object *shapeObject) Keys() []string {
	keys := make([]string, len(object.fields.keys))
	for index, key := range object.fields.keys {
		keys[index] = replaceClosureListenerMap(key, object.view.closureName)
	}
	return keys
}
func (object *shapeObject) Has(key string) bool    { _, ok := object.lookup(key); return ok }
func (*shapeObject) Set(string, goja.Value) bool   { return false }
func (object *shapeObject) Delete(key string) bool { return !object.Has(key) }

type shapeArray struct {
	view   *shapeView
	values []any
}

func (array *shapeArray) Len() int { return len(array.values) }
func (array *shapeArray) Get(index int) goja.Value {
	if index < 0 || index >= len(array.values) {
		return goja.Undefined()
	}
	return array.view.wrap(array.values[index])
}
func (*shapeArray) Set(int, goja.Value) bool { return false }
func (*shapeArray) SetLen(int) bool          { return false }

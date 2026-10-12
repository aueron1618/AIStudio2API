package waa

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"weak"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
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

func firefoxShape(vm *goja.Runtime) (goja.Value, error) {
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
	return wrapShapeValue(vm, data, data.value), nil
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

func wrapShapeValue(vm *goja.Runtime, owner *shapeData, value any) goja.Value {
	switch value := value.(type) {
	case *shapeFields:
		return vm.NewDynamicObject(&shapeObject{vm: vm, owner: owner, fields: value})
	case []any:
		return vm.NewDynamicArray(&shapeArray{vm: vm, owner: owner, values: value})
	default:
		return vm.ToValue(value)
	}
}

type shapeObject struct {
	vm     *goja.Runtime
	owner  *shapeData
	fields *shapeFields
}

func (object *shapeObject) Get(key string) goja.Value {
	value, ok := object.fields.values[key]
	if !ok {
		return nil
	}
	return wrapShapeValue(object.vm, object.owner, value)
}
func (object *shapeObject) Has(key string) bool    { _, ok := object.fields.values[key]; return ok }
func (object *shapeObject) Keys() []string         { return append([]string(nil), object.fields.keys...) }
func (*shapeObject) Set(string, goja.Value) bool   { return false }
func (object *shapeObject) Delete(key string) bool { return !object.Has(key) }

type shapeArray struct {
	vm     *goja.Runtime
	owner  *shapeData
	values []any
}

func (array *shapeArray) Len() int { return len(array.values) }
func (array *shapeArray) Get(index int) goja.Value {
	if index < 0 || index >= len(array.values) {
		return goja.Undefined()
	}
	return wrapShapeValue(array.vm, array.owner, array.values[index])
}
func (*shapeArray) Set(int, goja.Value) bool { return false }
func (*shapeArray) SetLen(int) bool          { return false }

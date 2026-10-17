package main

import (
	"fmt"
	"reflect"
	"sort"
)

// validateDescriptorConflicts 防止生成树按名字覆盖不同定义；相同结构允许来自重复模块。
func validateDescriptorConflicts(messages []Message, enums []Enum, resolver *TypeResolver, graph *webpackModuleGraph) error {
	seenMessages := make(map[string]Message)
	for _, message := range messages {
		// 标准包始终使用官方 proto，不生成客户端自带的重复运行时描述。
		if isGooglePkg(message.Package) {
			continue
		}
		previous, exists := seenMessages[message.TypeName]
		if !exists {
			seenMessages[message.TypeName] = message
			continue
		}
		before, err := canonicalDescriptorFields(previous, resolver, graph)
		if err != nil {
			return err
		}
		after, err := canonicalDescriptorFields(message, resolver, graph)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("消息 %s 存在冲突定义，拒绝覆盖字段", message.TypeName)
		}
	}
	seenEnums := make(map[string][]EnumValue)
	for _, enum := range enums {
		if isGooglePkg(enum.Package) {
			continue
		}
		if _, exists := seenMessages[enum.TypeName]; exists {
			return fmt.Errorf("类型 %s 同时定义为消息和枚举", enum.TypeName)
		}
		values := append([]EnumValue{}, enum.Values...)
		sort.Slice(values, func(i, j int) bool {
			if values[i].No != values[j].No {
				return values[i].No < values[j].No
			}
			return values[i].Name < values[j].Name
		})
		if previous, exists := seenEnums[enum.TypeName]; exists && !reflect.DeepEqual(previous, values) {
			return fmt.Errorf("枚举 %s 存在冲突定义", enum.TypeName)
		}
		seenEnums[enum.TypeName] = values
	}
	return nil
}

func canonicalDescriptorFields(message Message, resolver *TypeResolver, graph *webpackModuleGraph) ([]Field, error) {
	fields := append([]Field{}, message.Fields...)
	for i := range fields {
		field := &fields[i]
		kind, value := field.Kind, &field.T
		if field.Kind == "map" {
			kind, value = field.MapValueKind, &field.MapValueT
		}
		if kind != "message" && kind != "enum" {
			continue
		}
		ref, ok := (*value).(string)
		if !ok {
			return nil, fmt.Errorf("消息 %s 字段 %s 类型引用无效", message.TypeName, field.Name)
		}
		resolved, err := resolveTypeNameWithGraph(ref, message.Pos, message.ModuleStart, resolver, message.Package, kind, graph)
		if err != nil {
			return nil, fmt.Errorf("消息 %s 字段 %s: %w", message.TypeName, field.Name, err)
		}
		*value = resolved
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].No < fields[j].No })
	return fields, nil
}

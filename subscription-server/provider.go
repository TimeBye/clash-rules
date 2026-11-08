package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"gopkg.in/yaml.v3"
)

// proxyProvider 获取并清洗指定 provider 的订阅，不做模板合并。
func (s *service) proxyProvider(ctx context.Context, name string) ([]byte, error) {
	root, err := s.loadTemplate()
	if err != nil {
		return nil, err
	}
	providers := field(root, "proxy-providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		return nil, errors.New("模板缺少 proxy-providers 映射")
	}
	provider := field(providers, name)
	if provider == nil || provider.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("模板中不存在 provider %q", name)
	}
	endpoint := field(provider, "url")
	if endpoint == nil || endpoint.Tag != "!!str" || endpoint.Value == "" {
		return nil, fmt.Errorf("provider %q 缺少有效 url", name)
	}
	data, err := s.fetch(ctx, endpoint.Value, upstreamUA)
	if err != nil {
		return nil, fmt.Errorf("获取 provider %q 失败: %w", name, err)
	}
	providerRoot, err := parseMapping(data)
	if err != nil {
		return nil, errors.New("provider 响应不是有效的 YAML 映射")
	}
	filterProxies(providerRoot, "网站访问地址")
	data, err = encodeMapping(providerRoot)
	if err != nil {
		return nil, errors.New("无法编码 provider YAML")
	}
	return data, nil
}

// filterProxies 移除 proxies 数组及 proxy-groups 各分组的 proxies 子数组中，
// 名称包含 keyword 的节点，其余结构与字段保持不变。
func filterProxies(root *yaml.Node, keyword string) {
	removed := make(map[string]bool)
	if proxies := field(root, "proxies"); proxies != nil && proxies.Kind == yaml.SequenceNode {
		proxies.Content = filterNamedSequence(proxies.Content, keyword, removed)
	}
	if groups := field(root, "proxy-groups"); groups != nil && groups.Kind == yaml.SequenceNode {
		for i, group := range groups.Content {
			if group.Kind != yaml.MappingNode {
				continue
			}
			if proxies := field(group, "proxies"); proxies != nil && proxies.Kind == yaml.SequenceNode {
				before := len(proxies.Content)
				proxies.Content = filterNamedSequence(proxies.Content, keyword, removed)
				use := field(group, "use")
				includeAll := false
				for _, key := range []string{"include-all", "include-all-proxies", "include-all-providers"} {
					if value := field(group, key); value != nil && value.Tag == "!!bool" && value.Value == "true" {
						includeAll = true
					}
				}
				if before > 0 && len(proxies.Content) == 0 && (use == nil || len(use.Content) == 0) && !includeAll {
					log.Printf("provider 过滤告警: 第 %d 个 proxy-group 的 proxies 已被清空", i+1)
				}
			}
		}
	}
	if rules := field(root, "rules"); rules != nil && rules.Kind == yaml.SequenceNode {
		for i, rule := range rules.Content {
			if rule.Kind != yaml.ScalarNode || rule.Tag != "!!str" {
				continue
			}
			parts := strings.Split(rule.Value, ",")
			last := len(parts) - 1
			if last > 0 && strings.TrimSpace(parts[last]) == "no-resolve" {
				last--
			}
			if last > 0 && removed[strings.TrimSpace(parts[last])] {
				log.Printf("provider 过滤告警: 第 %d 条规则仍引用已过滤的节点", i+1)
			}
		}
	}
}

// filterNamedSequence 过滤序列元素：字符串标量直接匹配值；映射节点匹配其 name 字段。
func filterNamedSequence(items []*yaml.Node, keyword string, removed map[string]bool) []*yaml.Node {
	filtered := make([]*yaml.Node, 0, len(items))
	for _, item := range items {
		if itemNameMatches(item, keyword) {
			name := item
			if item.Kind == yaml.MappingNode {
				name = field(item, "name")
			}
			removed[name.Value] = true
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// itemNameMatches 判断序列元素的名称是否包含 keyword。
func itemNameMatches(item *yaml.Node, keyword string) bool {
	switch item.Kind {
	case yaml.ScalarNode:
		return strings.Contains(item.Value, keyword)
	case yaml.MappingNode:
		name := field(item, "name")
		return name != nil && name.Tag == "!!str" && strings.Contains(name.Value, keyword)
	}
	return false
}

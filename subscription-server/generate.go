package main

import (
	"context"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

func (s *service) generate(ctx context.Context) ([]byte, error) {
	root, err := s.loadTemplate()
	if err != nil {
		return nil, err
	}
	providers := field(root, "proxy-providers")
	if providers == nil || providers.Kind != yaml.MappingNode || len(providers.Content) < 4 {
		return nil, errors.New("模板 proxy-providers 必须是包含至少两个 provider 的映射")
	}
	// 映射节点按键、值交替排列，Content[3] 是第二个 provider 的配置。
	second := providers.Content[3]
	endpoint := field(second, "url")
	if endpoint == nil || endpoint.Tag != "!!str" || endpoint.Value == "" {
		return nil, errors.New("第二个 provider 缺少有效 url")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		index int
		root  *yaml.Node
		err   error
	}
	results := make(chan result, 2)
	for i, source := range []struct{ url, ua, name, key string }{
		{endpoint.Value, upstreamUA, "第二个 provider", "dns"},
		{s.hostsURL, "", "远程 hosts", "payload"},
	} {
		go func(index int, address, ua, name, key string) {
			data, err := s.fetch(ctx, address, ua)
			var node *yaml.Node
			if err != nil {
				err = fmt.Errorf("获取%s失败: %w", name, err)
			} else if node, err = parseMapping(data); err != nil {
				err = fmt.Errorf("%s响应不是有效的 YAML 映射", name)
			} else if value := field(node, key); value == nil || value.Kind != yaml.MappingNode || (key == "dns" && len(value.Content) == 0) {
				err = fmt.Errorf("%s缺少有效的 %s 映射", name, key)
				if key == "dns" {
					err = errors.New("第二个 provider 缺少有效的非空 dns 映射，无法覆盖模板 DNS")
				}
			}
			results <- result{index, node, err}
		}(i, source.url, source.ua, source.name, source.key)
	}
	var nodes [2]*yaml.Node
	var downloadErr error
	for range nodes {
		result := <-results
		if result.err != nil && downloadErr == nil {
			downloadErr = result.err
			cancel()
		}
		nodes[result.index] = result.root
	}
	if downloadErr != nil {
		return nil, downloadErr
	}
	provider := nodes[0]
	dns := field(provider, "dns")
	payload := field(nodes[1], "payload")
	hosts := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	sources := []struct {
		node *yaml.Node
		name string
	}{
		{field(root, "hosts"), "模板"},
		{payload, "远程 hosts"},
		{field(provider, "hosts"), "provider"},
	}
	for _, source := range sources {
		if source.node == nil {
			continue
		}
		if source.node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s 的 hosts 必须是映射", source.name)
		}
		for i := 0; i < len(source.node.Content); i += 2 {
			setField(hosts, source.node.Content[i].Value, source.node.Content[i+1])
		}
	}
	setField(root, "hosts", hosts)
	setField(root, "dns", dns)
	data, err := encodeMapping(root)
	if err != nil {
		return nil, errors.New("无法编码订阅 YAML")
	}
	return data, nil
}

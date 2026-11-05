package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const defaultHostsURL = "https://raw.githubusercontent.com/TimeBye/clash-rules/refs/heads/own/hosts.txt"
const maxDownload = 16 << 20

type service struct {
	template string
	hostsURL string
	client   *http.Client
}

func main() {
	template := flag.String("template", "config/youknow.yaml", "模板 YAML 路径（相对于工作目录）")
	hostsURL := flag.String("hosts-url", defaultHostsURL, "远程 hosts YAML 地址（payload 映射）")
	listen := flag.String("listen", "0.0.0.0:8080", "HTTP 监听地址")
	subscribePath := flag.String("subscribe-path", "/youknow", "订阅接口路径（以 / 开头）")
	flag.Parse()
	s := &service{*template, *hostsURL, &http.Client{Timeout: 30 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc(*subscribePath, s.subscribe)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message strings.Builder
		fmt.Fprintf(&message, "收到请求 method=%q path=%q remote=%q host=%q\n请求头:", r.Method, r.URL.Path, r.RemoteAddr, r.Host)
		names := make([]string, 0, len(r.Header))
		for name := range r.Header {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&message, "\n  %s: %q", name, r.Header[name])
		}
		log.Print(message.String())
		mux.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("订阅服务监听 %s，接口 %s", *listen, *subscribePath)
	log.Fatal(server.ListenAndServe())
}

func (s *service) subscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "仅支持 GET", http.StatusMethodNotAllowed)
		return
	}
	ua := strings.ToLower(r.UserAgent())
	if !strings.Contains(ua, "clash.meta") && !strings.Contains(ua, "clashmeta") && !strings.Contains(ua, "mihomo") {
		http.Redirect(w, r, "https://www.baidu.com", http.StatusMovedPermanently)
		return
	}
	data, status, err := s.generate(r.Context())
	if err != nil {
		log.Printf("生成订阅失败: %v", err)
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(data)
}

func (s *service) generate(ctx context.Context) ([]byte, int, error) {
	data, err := os.ReadFile(s.template)
	if err != nil {
		return nil, 500, errors.New("无法读取模板文件")
	}
	root, err := parseMapping(data)
	if err != nil {
		return nil, 500, errors.New("模板必须是有效的 YAML 映射")
	}
	providers := field(root, "proxy-providers")
	if providers == nil || providers.Kind != yaml.MappingNode || len(providers.Content) < 4 {
		return nil, 500, errors.New("模板 proxy-providers 必须是包含至少两个 provider 的映射")
	}
	// 映射节点按键、值交替排列，Content[3] 是第二个 provider 的配置。
	second := providers.Content[3]
	endpoint := field(second, "url")
	if endpoint == nil || endpoint.Tag != "!!str" || endpoint.Value == "" {
		return nil, 500, errors.New("第二个 provider 缺少有效 url")
	}
	data, err = s.fetch(ctx, endpoint.Value, "ClashMeta/1.19.33; mihomo/1.19.33")
	if err != nil {
		return nil, 502, fmt.Errorf("获取第二个 provider 失败: %w", err)
	}
	provider, err := parseMapping(data)
	if err != nil {
		return nil, 502, errors.New("provider 响应不是有效的 YAML 映射")
	}
	dns := field(provider, "dns")
	if dns == nil || dns.Kind != yaml.MappingNode || len(dns.Content) == 0 {
		return nil, 502, errors.New("第二个 provider 缺少有效的非空 dns 映射，无法覆盖模板 DNS")
	}
	data, err = s.fetch(ctx, s.hostsURL, "")
	if err != nil {
		return nil, 502, fmt.Errorf("获取远程 hosts 失败: %w", err)
	}
	remote, err := parseMapping(data)
	if err != nil {
		return nil, 502, errors.New("远程 hosts 响应不是有效 YAML")
	}
	payload := field(remote, "payload")
	if payload == nil || payload.Kind != yaml.MappingNode {
		return nil, 502, errors.New("远程 hosts 必须包含 payload 映射")
	}
	hosts := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	sources := []struct {
		node   *yaml.Node
		name   string
		status int
	}{
		{field(root, "hosts"), "模板", 500},
		{payload, "远程 hosts", 502},
		{field(provider, "hosts"), "provider", 502},
	}
	for _, source := range sources {
		if source.node == nil {
			continue
		}
		if source.node.Kind != yaml.MappingNode {
			return nil, source.status, fmt.Errorf("%s 的 hosts 必须是映射", source.name)
		}
		for i := 0; i < len(source.node.Content); i += 2 {
			setField(hosts, source.node.Content[i].Value, source.node.Content[i+1])
		}
	}
	setField(root, "hosts", hosts)
	setField(root, "dns", dns)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, 500, errors.New("无法编码订阅 YAML")
	}
	enc.Close()
	return readableUnicode(out.Bytes()), http.StatusOK, nil
}

// 仅处理编码结果中双引号标量的 Unicode 转义，跳过字面反斜杠、注释和块文本。
func readableUnicode(data []byte) []byte {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return data
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	starts := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		starts[i] = starts[i-1] + len(lines[i-1])
	}
	quoted := make(map[int]bool)
	var visit func(*yaml.Node)
	visit = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && n.Style&yaml.DoubleQuotedStyle != 0 {
			line := lines[n.Line-1]
			offset := 0
			for col := 1; col < n.Column; col++ {
				_, size := utf8.DecodeRune(line[offset:])
				offset += size
			}
			quoted[starts[n.Line-1]+offset] = true
		}
		for _, child := range n.Content {
			visit(child)
		}
	}
	visit(&doc)
	var out bytes.Buffer
	inside := false
	for i := 0; i < len(data); {
		if !inside {
			inside = quoted[i]
			out.WriteByte(data[i])
			i++
			continue
		}
		if data[i] == '"' {
			inside = false
		}
		if data[i] == '\\' && i+1 < len(data) {
			if data[i+1] == 'U' && i+10 <= len(data) {
				value, err := strconv.ParseUint(string(data[i+2:i+10]), 16, 32)
				if err == nil && value >= 0x10000 && value <= utf8.MaxRune {
					out.WriteRune(rune(value))
					i += 10
					continue
				}
			}
			out.Write(data[i : i+2])
			i += 2
			continue
		}
		out.WriteByte(data[i])
		i++
	}
	return out.Bytes()
}

func (s *service) fetch(ctx context.Context, address, ua string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("地址必须是有效的 HTTP(S) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("无法创建上游请求")
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := s.client.Do(req)
	// 不返回原始网络错误，避免在日志和 HTTP 响应中泄漏订阅 URL 的令牌。
	if err != nil {
		return nil, errors.New("网络请求失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游 HTTP 状态码 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, errors.New("读取上游响应失败")
	}
	if len(data) > maxDownload {
		return nil, errors.New("上游响应超过 16 MiB")
	}
	return data, nil
}

func parseMapping(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("需要 YAML 映射")
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("只允许一个 YAML 文档")
	}
	root := doc.Content[0]
	if err := validateNode(root); err != nil {
		return nil, err
	}
	return root, nil
}

func validateNode(n *yaml.Node) error {
	// 合并不同文档时锚点可能冲突，拒绝别名，避免生成含悬空引用的配置。
	if n.Kind == yaml.AliasNode {
		return errors.New("不支持 YAML 别名")
	}
	n.Anchor = ""
	if n.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return errors.New("映射键必须为不重复的字符串")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := validateNode(child); err != nil {
			return err
		}
	}
	return nil
}

func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func setField(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

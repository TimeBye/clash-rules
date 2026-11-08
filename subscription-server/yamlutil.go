package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func (s *service) loadTemplate() (*yaml.Node, error) {
	data, err := os.ReadFile(s.template)
	if err != nil {
		return nil, errors.New("无法读取模板文件")
	}
	root, err := parseMapping(data)
	if err != nil {
		return nil, errors.New("模板必须是有效的 YAML 映射")
	}
	return root, nil
}

func encodeMapping(root *yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return readableUnicode(out.Bytes()), nil
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

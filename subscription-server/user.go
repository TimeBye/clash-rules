package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

type userEntry struct {
	token    string
	expire   time.Time // 有效期截止时间（到期日次日零点，到期日当天仍可用）
	provider string    // 指定 provider 时直接返回其上游内容；为空则走模板合并
	expired  bool      // 运行时标记：当前请求的用户是否已过期
}

func loadUsers(path string) ([]userEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Users []struct {
			Token    string `yaml:"token"`
			Expire   string `yaml:"expire"`
			Provider string `yaml:"provider"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errors.New("用户配置不是有效的 YAML 或字段类型不正确")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, errors.New("无法加载北京时间时区")
	}
	users := make([]userEntry, 0, len(doc.Users))
	seen := make(map[string]int, len(doc.Users))
	for i, u := range doc.Users {
		if u.Token == "" {
			return nil, fmt.Errorf("第 %d 个用户的 token 不能为空", i+1)
		}
		if previous, ok := seen[u.Token]; ok {
			return nil, fmt.Errorf("第 %d 个用户与第 %d 个用户的 token 重复", i+1, previous)
		}
		seen[u.Token] = i + 1
		day, err := time.ParseInLocation("2006-01-02", u.Expire, location)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个用户的 expire 须为 YYYY-MM-DD", i+1)
		}
		users = append(users, userEntry{token: u.Token, expire: day.AddDate(0, 0, 1), provider: strings.TrimSpace(u.Provider)})
	}
	return users, nil
}

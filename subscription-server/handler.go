package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const expiredClashConfig = `mode: rule
proxies: []
proxy-providers: {}
proxy-groups:
  - name: 订阅已过期
    type: select
    proxies:
      - DIRECT
rules:
  - MATCH,订阅已过期
`

func (s *service) subscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		redirectBaidu(w, r)
		return
	}
	ua := strings.ToLower(r.UserAgent())
	if !strings.Contains(ua, "clash.meta") && !strings.Contains(ua, "clashmeta") && !strings.Contains(ua, "mihomo") {
		redirectBaidu(w, r)
		return
	}
	user, err := s.authorize(r)
	if err != nil {
		log.Printf("用户认证失败: %v", err)
		redirectBaidu(w, r)
		return
	}
	if user != nil && user.provider != "" {
		w.Header().Set("subscription-userinfo", fmt.Sprintf("expire=%d", user.expire.Unix()))
	}
	if user != nil && user.expired {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		fmt.Fprint(w, expiredClashConfig)
		log.Print("用户订阅已过期，已返回空白配置（无可用节点，所有流量 DIRECT 直连）")
		return
	}
	var data []byte
	if user != nil && user.provider != "" {
		data, err = s.proxyProvider(r.Context(), user.provider)
	} else {
		data, err = s.generate(r.Context())
	}
	if err != nil {
		log.Printf("生成订阅失败: %v", err)
		redirectBaidu(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(data)
}

// redirectBaidu 将请求重定向到百度。
func redirectBaidu(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://www.baidu.com", http.StatusMovedPermanently)
}

// 校验请求中的用户令牌。返回匹配的用户条目（验证关闭时为 nil）和错误。
func (s *service) authorize(r *http.Request) (*userEntry, error) {
	if s.usersPath == "" {
		return nil, nil
	}
	users, err := loadUsers(s.usersPath)
	if err != nil {
		return nil, fmt.Errorf("读取用户配置失败: %w", err)
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return nil, errors.New("缺少用户令牌")
	}
	for _, u := range users {
		if u.token != token {
			continue
		}
		u.expired = !time.Now().Before(u.expire)
		return &u, nil
	}
	return nil, errors.New("无效的用户令牌")
}

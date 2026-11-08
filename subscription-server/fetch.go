package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

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

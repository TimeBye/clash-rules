package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const defaultHostsURL = "https://raw.githubusercontent.com/TimeBye/clash-rules/refs/heads/own/hosts.txt"
const maxDownload = 16 << 20
const upstreamUA = "ClashMeta/1.19.33; mihomo/1.19.33"

type service struct {
	template  string
	hostsURL  string
	usersPath string
	client    *http.Client
}

func main() {
	template := flag.String("template", "config/youknow.yaml", "模板 YAML 路径（相对于工作目录）")
	hostsURL := flag.String("hosts-url", defaultHostsURL, "远程 hosts YAML 地址（payload 映射）")
	usersPath := flag.String("users", "config/users.yaml", "用户及有效期 YAML 路径（置空则关闭验证）")
	listen := flag.String("listen", "0.0.0.0:8080", "HTTP 监听地址")
	subscribePath := flag.String("subscribe-path", "/youknow", "订阅接口路径（以 / 开头）")
	flag.Parse()
	s := &service{template: *template, hostsURL: *hostsURL, usersPath: *usersPath, client: &http.Client{Timeout: 30 * time.Second}}
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

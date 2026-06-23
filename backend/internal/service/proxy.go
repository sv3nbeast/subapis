package service

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Proxy struct {
	ID        int64
	Name      string
	Protocol  string
	Host      string
	Port      int
	Username  string
	Password  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (p *Proxy) IsActive() bool {
	return p.Status == StatusActive
}

func (p *Proxy) URL() string {
	scheme := p.EffectiveProtocol()
	if scheme == "socks5" {
		scheme = "socks5h"
	}
	host := normalizeProxyHost(p.Host)
	port := p.Port
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
		port = 0
	}
	urlHost := host
	if port > 0 {
		urlHost = net.JoinHostPort(host, strconv.Itoa(port))
	} else if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		urlHost = "[" + host + "]"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   urlHost,
	}
	if p.Username != "" && p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String()
}

func normalizeProxyHost(host string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
}

func (p *Proxy) EffectiveProtocol() string {
	if p == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(p.Protocol))
	if scheme == "socks5" {
		return "socks5h"
	}
	return scheme
}

type ProxyWithAccountCount struct {
	Proxy
	AccountCount   int64
	LatencyMs      *int64
	LatencyStatus  string
	LatencyMessage string
	IPAddress      string
	Country        string
	CountryCode    string
	Region         string
	City           string
	QualityStatus  string
	QualityScore   *int
	QualityGrade   string
	QualitySummary string
	QualityChecked *int64
}

type ProxyAccountSummary struct {
	ID       int64
	Name     string
	Platform string
	Type     string
	Notes    *string
}

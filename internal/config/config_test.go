package config

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseCIDRs(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantNets    []string
		wantInvalid []string
		wantWide    []string
	}{
		{name: "未設定は空（誰も信頼しない）", in: "", wantNets: nil},
		{name: "1 件", in: "100.101.188.8/32", wantNets: []string{"100.101.188.8/32"}},
		{name: "カンマ区切りと空白と空の要素", in: " 100.64.0.0/10 , ,fd00::/8,", wantNets: []string{"100.64.0.0/10", "fd00::/8"}, wantWide: []string{"100.64.0.0/10", "fd00::/8"}},
		{name: "IP だけは 1 台の範囲", in: "100.101.188.8,2001:db8::1", wantNets: []string{"100.101.188.8/32", "2001:db8::1/128"}},
		{name: "解釈できない要素は捨てて報告", in: "100.101.188.8/32,bogus,10.0.0.0/33", wantNets: []string{"100.101.188.8/32"}, wantInvalid: []string{"bogus", "10.0.0.0/33"}},
		{name: "ホスト部分が 0 でない要素は捨てる（/32 の書き間違い）", in: "100.101.188.8/10,2001:db8::1/64", wantInvalid: []string{"100.101.188.8/10", "2001:db8::1/64"}},
		{name: "IPv4 の /24 と IPv6 の /64 は広すぎない", in: "192.0.2.0/24,2001:db8::/64", wantNets: []string{"192.0.2.0/24", "2001:db8::/64"}},
		{name: "IPv4 で /24、IPv6 で /64 より広いものは使うが報告", in: "100.64.0.0/10,0.0.0.0/0,fd00::/8", wantNets: []string{"100.64.0.0/10", "0.0.0.0/0", "fd00::/8"}, wantWide: []string{"100.64.0.0/10", "0.0.0.0/0", "fd00::/8"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nets, invalid, wide := ParseCIDRs(tt.in)
			var got []string
			for _, n := range nets {
				got = append(got, n.String())
			}
			if !reflect.DeepEqual(got, tt.wantNets) {
				t.Errorf("nets = %v, want %v", got, tt.wantNets)
			}
			if !reflect.DeepEqual(invalid, tt.wantInvalid) {
				t.Errorf("invalid = %v, want %v", invalid, tt.wantInvalid)
			}
			if !reflect.DeepEqual(wide, tt.wantWide) {
				t.Errorf("wide = %v, want %v", wide, tt.wantWide)
			}
		})
	}
}

func TestLoad_TrustedProxyCIDRs(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	if cfg := Load(); len(cfg.TrustedProxies) != 0 {
		t.Errorf("unset TRUSTED_PROXY_CIDRS: got %v, want empty", cfg.TrustedProxies)
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "100.101.188.8/32")
	cfg := Load()
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0].String() != "100.101.188.8/32" {
		t.Errorf("TrustedProxies = %v, want [100.101.188.8/32]", cfg.TrustedProxies)
	}
}

func TestLoad_WSMaxConnections(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{in: "", want: DefaultWSMaxConnections},
		{in: "50", want: 50},
		{in: "0", want: DefaultWSMaxConnections},
		{in: "-1", want: DefaultWSMaxConnections},
		{in: "abc", want: DefaultWSMaxConnections},
	}
	for _, tt := range tests {
		t.Setenv("WS_MAX_CONNECTIONS", tt.in)
		if got := Load().WSMaxConnections; got != tt.want {
			t.Errorf("WS_MAX_CONNECTIONS=%q: got %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantOrigins  []string
		wantRejected []string
	}{
		{name: "通常", in: "https://a.example,https://b.example", wantOrigins: []string{"https://a.example", "https://b.example"}},
		{name: "空白と空の要素を捨てる", in: " https://a.example , ,https://b.example,", wantOrigins: []string{"https://a.example", "https://b.example"}},
		{name: "カンマだけ", in: ",", wantOrigins: []string{}},
		{name: "* は拒否", in: "*", wantOrigins: []string{}, wantRejected: []string{"*"}},
		{name: "* を含む要素も拒否", in: "https://a.example,https://*.example", wantOrigins: []string{"https://a.example"}, wantRejected: []string{"https://*.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origins, rejected := ParseAllowedOrigins(tt.in)
			if !reflect.DeepEqual(origins, tt.wantOrigins) {
				t.Errorf("origins = %q, want %q", origins, tt.wantOrigins)
			}
			if !reflect.DeepEqual(rejected, tt.wantRejected) {
				t.Errorf("rejected = %q, want %q", rejected, tt.wantRejected)
			}
		})
	}
}

func TestLoad_AllowedOrigins(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	if got := Load().AllowedOrigins; !reflect.DeepEqual(got, []string{"http://localhost:3000", "http://127.0.0.1:3000"}) {
		t.Errorf("default AllowedOrigins = %q", got)
	}

	t.Setenv("ALLOWED_ORIGINS", "https://a.example,,*")
	if got := Load().AllowedOrigins; !reflect.DeepEqual(got, []string{"https://a.example"}) {
		t.Errorf("AllowedOrigins = %q, want [https://a.example]", got)
	}
}

// 本番で TRUSTED_PROXY_CIDRS が未設定なら、起動時に警告を出すこと（起動は止めない）
func TestLoad_ProductionWarnsWithoutTrustedProxies(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("ENV", "production")
	Load()
	if !strings.Contains(buf.String(), "TRUSTED_PROXY_CIDRS is not set in production") {
		t.Errorf("production without TRUSTED_PROXY_CIDRS should warn, log = %q", buf.String())
	}

	buf.Reset()
	t.Setenv("TRUSTED_PROXY_CIDRS", "100.101.188.8/32")
	Load()
	if strings.Contains(buf.String(), "not set in production") {
		t.Errorf("should not warn when set, log = %q", buf.String())
	}

	buf.Reset()
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("ENV", "development")
	Load()
	if strings.Contains(buf.String(), "not set in production") {
		t.Errorf("should not warn in development, log = %q", buf.String())
	}
}

package main

import (
	"reflect"
	"testing"

	"pvfine/internal/mcp"
)

// TestParseMCPAddrs 覆盖 PVFINE_MCP_ADDR 的三种写法（单地址 / 逗号列表 / 端口区间）
// 以及非法项处理：非法项只跳过，不影响同批次的合法项。
func TestParseMCPAddrs(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		want         []string
		wantRejected int
	}{
		{
			name: "单个地址",
			raw:  "127.0.0.1:17650",
			want: []string{"127.0.0.1:17650"},
		},
		{
			name: "逗号多个地址",
			raw:  "127.0.0.1:8000,127.0.0.1:9000",
			want: []string{"127.0.0.1:8000", "127.0.0.1:9000"},
		},
		{
			name: "端口区间含首尾",
			raw:  "127.0.0.1:8000-8004",
			want: []string{
				"127.0.0.1:8000", "127.0.0.1:8001", "127.0.0.1:8002",
				"127.0.0.1:8003", "127.0.0.1:8004",
			},
		},
		{
			name: "区间与单项混用",
			raw:  "127.0.0.1:8000-8001,127.0.0.1:9000",
			want: []string{"127.0.0.1:8000", "127.0.0.1:8001", "127.0.0.1:9000"},
		},
		{
			name: "空白与空项被忽略且去重保序",
			raw:  "  127.0.0.1:9000 , , 127.0.0.1:8000 ,127.0.0.1:9000 ",
			want: []string{"127.0.0.1:9000", "127.0.0.1:8000"},
		},
		{
			name: "IPv6 需带方括号",
			raw:  "[::1]:8000",
			want: []string{"[::1]:8000"},
		},
		{name: "非法：缺少 host", raw: ":8000", wantRejected: 1},
		{name: "非法：缺少端口", raw: "127.0.0.1:", wantRejected: 1},
		{name: "非法：端口非数字", raw: "127.0.0.1:abc", wantRejected: 1},
		{name: "非法：端口越界", raw: "127.0.0.1:70000", wantRejected: 1},
		{name: "非法：端口为 0", raw: "127.0.0.1:0", wantRejected: 1},
		{name: "非法：区间倒序", raw: "127.0.0.1:8004-8000", wantRejected: 1},
		{
			// 误填整段端口范围时必须整项丢弃，否则会把端口占满。
			name:         "非法：区间超过上限",
			raw:          "127.0.0.1:1-65535",
			wantRejected: 1,
		},
		{
			name:         "非法项与合法项共存时只丢非法项",
			raw:          "127.0.0.1:8000,:1",
			want:         []string{"127.0.0.1:8000"},
			wantRejected: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rejected := parseMCPAddrs(tc.raw)
			if len(rejected) != tc.wantRejected {
				t.Fatalf("非法的项数 = %d (%v)，期望 %d", len(rejected), rejected, tc.wantRejected)
			}
			if tc.want == nil {
				tc.want = []string{}
			}
			if got == nil {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("解析结果 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestMCPListenAddrsFivePorts 是本轮需求的直接断言：8000-8004 展开成 5 个回环地址。
func TestMCPListenAddrsFivePorts(t *testing.T) {
	t.Setenv(mcpAddrEnvName, "127.0.0.1:8000-8004")
	want := []string{
		"127.0.0.1:8000", "127.0.0.1:8001", "127.0.0.1:8002",
		"127.0.0.1:8003", "127.0.0.1:8004",
	}
	got := mcpListenAddrs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("监听地址 = %v，期望 %v（%d 个端口）", got, want, len(want))
	}
}

// TestMCPListenAddrsFallback 未配置或全非法时，必须退回内核默认地址（保持既有行为）。
func TestMCPListenAddrsFallback(t *testing.T) {
	for _, raw := range []string{"", "   ", "nonsense", ":1,127.0.0.1:0"} {
		t.Run("取值="+raw, func(t *testing.T) {
			t.Setenv(mcpAddrEnvName, raw)
			got := mcpListenAddrs()
			if len(got) != 1 || got[0] != mcp.Addr() {
				t.Fatalf("监听地址 = %v，期望退回默认 %q", got, mcp.Addr())
			}
		})
	}
}

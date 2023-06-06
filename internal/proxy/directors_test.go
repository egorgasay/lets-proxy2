package proxy

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/rekby/lets-proxy2/internal/contextlabel"

	"github.com/gojuno/minimock/v3"

	"github.com/rekby/lets-proxy2/internal/th"

	"github.com/maxatome/go-testdeep"
)

func TestDirectorChain(t *testing.T) {
	mc := minimock.NewController(t)
	defer mc.Finish()

	var chain = NewDirectorChain()
	req := &http.Request{}
	chain.Director(req)

	d1 := NewDirectorMock(mc)
	d1.DirectorMock.Expect(req).Return(nil)
	d2 := NewDirectorMock(mc)
	d2.DirectorMock.Expect(req).Return(nil)
	chain = NewDirectorChain(d1, d2)
	chain.Director(req)
}

func TestDirectorDestMap(t *testing.T) {
	ctx, flush := th.TestContext(t)
	defer flush()

	td := testdeep.NewT(t)

	var req *http.Request

	m := map[string]string{
		(&net.TCPAddr{IP: net.ParseIP("1.2.3.1"), Port: 443}).String():        "1.1.1.1:80",
		(&net.TCPAddr{IP: net.ParseIP("1.2.3.2"), Port: 443}).String():        "2.2.2.2:80",
		(&net.TCPAddr{IP: net.ParseIP("::ffff:1.2.3.1"), Port: 443}).String(): "3.3.3.3:80",
	}

	d := NewDirectorDestMap(m)

	req = &http.Request{}
	req = req.WithContext(context.WithValue(
		ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("aaa"), Port: 881}))
	d.Director(req)
	td.Nil(req.URL)

	req = &http.Request{}
	req = req.WithContext(context.WithValue(
		ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("8.8.8.8"), Port: 443}))
	d.Director(req)
	td.Nil(req.URL)

	req = &http.Request{}
	req = req.WithContext(context.WithValue(
		ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("1.2.3.1"), Port: 443}))
	d.Director(req)
	td.CmpDeeply(req.URL.Host, "3.3.3.3:80")

	req = &http.Request{}
	req = req.WithContext(context.WithValue(
		ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("1.2.3.2"), Port: 443}))
	d.Director(req)
	td.CmpDeeply(req.URL.Host, "2.2.2.2:80")
}

func TestDirectorHost(t *testing.T) {
	td := testdeep.NewT(t)

	d := NewDirectorHost("haha:81")
	req := &http.Request{}
	d.Director(req)
	td.CmpDeeply(req.URL.Host, "haha:81")
}

func TestDirectorSameIP(t *testing.T) {
	ctx, flush := th.TestContext(t)
	defer flush()

	td := testdeep.NewT(t)

	d := NewDirectorSameIP(87)
	req := &http.Request{}
	req = req.WithContext(context.WithValue(
		ctx, http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 881}))
	d.Director(req)
	td.CmpDeeply(req.URL.Host, "1.2.3.4:87")
}

func TestDirectorSetHeaders(t *testing.T) {
	ctx, flush := th.TestContext(t)
	defer flush()

	td := testdeep.NewT(t)

	m := map[string]string{
		"TestConnectionID": "{{CONNECTION_ID}}",
		"TestIP":           "{{SOURCE_IP}}",
		"TestPort":         "{{SOURCE_PORT}}",
		"TestIPPort":       "{{SOURCE_IP}}:{{SOURCE_PORT}}",
		"TestVal":          "ddd",
		"TestProtocol":     "{{HTTP_PROTO}}",
	}

	d := NewDirectorSetHeaders(m)

	ctx = context.WithValue(ctx, contextlabel.ConnectionID, "123")

	req := &http.Request{RemoteAddr: "1.2.3.4:881"}
	req = req.WithContext(ctx)
	d.Director(req)
	td.CmpDeeply(req.Header.Get("TestConnectionID"), "123")
	td.CmpDeeply(req.Header.Get("TestIP"), "1.2.3.4")
	td.CmpDeeply(req.Header.Get("TestPort"), "881")
	td.CmpDeeply(req.Header.Get("TestIPPort"), "1.2.3.4:881")
	td.CmpDeeply(req.Header.Get("TestVal"), "ddd")
	td.CmpDeeply(req.Header.Get("TestProtocol"), "error protocol detection")

	req = &http.Request{RemoteAddr: "1.2.3.4:881"}
	ctx = context.WithValue(ctx, contextlabel.TLSConnection, true)
	req = req.WithContext(ctx)
	d.Director(req)
	td.CmpDeeply(req.Header.Get("TestProtocol"), "https")

	req = &http.Request{RemoteAddr: "1.2.3.4:881"}
	ctx = context.WithValue(ctx, contextlabel.TLSConnection, false)
	req = req.WithContext(ctx)
	d.Director(req)
	td.CmpDeeply(req.Header.Get("TestProtocol"), "http")
}

func TestDirectorSetHeadersByIP(t *testing.T) {
	ctx, flush := th.TestContext(t)
	defer flush()

	type args struct {
		request *http.Request
	}

	m := map[string]HTTPHeaders{
		"192.168.0.0/24": {
			{Name: "TestHeader1", Value: "TestHeaderValue1"},
			{Name: "TestHeader2", Value: "TestHeaderValue2"},
			{Name: "TestHeader3", Value: "TestHeaderValue3"},
			{Name: "TestHeader4", Value: "TestHeaderValue4"},
		},
		"fe80:0000:0000:0000::/64": {
			{Name: "TestHeader5", Value: "TestHeaderValue5"},
		},
	}

	td := testdeep.NewT(t)
	d, err := NewDirectorSetHeadersByIP(m)
	td.CmpNoError(err)

	tests := []struct {
		name         string
		args         args
		shouldModify bool
		wantErr      bool
	}{
		{
			name: "okIPv4",
			args: args{
				request: &http.Request{RemoteAddr: "192.168.0.19:897"},
			},
			shouldModify: true,
		},
		{
			name: "okIPv6",
			args: args{
				request: &http.Request{RemoteAddr: "[fe80::28ca:829b:2d2e:a908]:897"},
			},
			shouldModify: true,
		},
		{
			name: "nilRequest",
			args: args{
				request: nil,
			},
			wantErr:      true,
			shouldModify: false,
		},
		{
			name: "wrongAddrIPv4",
			args: args{
				request: &http.Request{RemoteAddr: "172.168.0:897"},
			},
			shouldModify: false,
			wantErr:      true,
		},
		{
			name: "wrongAddrIPv6",
			args: args{
				request: &http.Request{RemoteAddr: "[ae80:28ca:27ca:829b:2d2e:a908]:897"},
			},
			shouldModify: false,
			wantErr:      true,
		},
		{
			name: "noPortIPv4",
			args: args{
				request: &http.Request{RemoteAddr: "172.168.0.1"},
			},
			shouldModify: false,
			wantErr:      true,
		},
		{
			name: "noPortIPv6",
			args: args{
				request: &http.Request{RemoteAddr: "[ae80:28ca:27ca:829b:2d2e:a908]"},
			},
			shouldModify: false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.args.request != nil {
				tt.args.request = tt.args.request.WithContext(ctx)
			}
			if err := d.Director(tt.args.request); (err != nil) != tt.wantErr {
				t.Errorf("Director() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr || !tt.shouldModify {
				return
			}

			var found bool
			for network, headers := range m {
				_, cidr, err := net.ParseCIDR(network)
				if err != nil {
					t.Errorf("ParseCIDR: %v", err)
				}

				split := strings.Split(tt.args.request.RemoteAddr, ":")
				ip := tt.args.request.RemoteAddr

				if len(split) > 1 {
					ip = strings.Trim(strings.Join(split[:len(split)-1], ":"), "[]")
				} else if len(split) == 0 {
					t.Errorf("Director() RemoteAddr error")
					continue
				}

				if !cidr.Contains(net.ParseIP(ip)) {
					continue
				}

				found = true
				for _, header := range headers {
					td.CmpDeeply(tt.args.request.Header.Get(header.Name), header.Value)
				}
			}

			if !found {
				t.Errorf("Director() headers not found")
			}

		})
	}
}

func TestNewDirectorSetHeadersByIP(t *testing.T) {
	ctx, flush := th.TestContext(t)
	defer flush()
	td := testdeep.NewT(t)

	type args struct {
		ctx context.Context
		m   map[string]HTTPHeaders
	}
	tests := []struct {
		name    string
		args    args
		ips     []net.IP
		wantErr bool
	}{
		{
			name: "ok",
			args: args{
				ctx: ctx,
				m: map[string]HTTPHeaders{
					"192.168.0.0/24": {
						{Name: "TestHeader1", Value: "TestHeaderValue1"},
						{Name: "TestHeader2", Value: "TestHeaderValue2"},
						{Name: "TestHeader3", Value: "TestHeaderValue3"},
						{Name: "TestHeader4", Value: "TestHeaderValue4"},
					},
					"fe80::/64": {
						{Name: "TestHeader5", Value: "TestHeaderValue5"},
					},
				},
			},
			ips: []net.IP{net.ParseIP("192.168.0.1"), net.ParseIP("fe80:0000:0000:0000::1")},
		},
		{
			name: "wrongFormat",
			args: args{
				ctx: ctx,
				m: map[string]HTTPHeaders{
					"192.168.0.v": {
						{Name: "TestHeader1", Value: "TestHeaderValue1"},
						{Name: "TestHeader2", Value: "TestHeaderValue2"},
						{Name: "TestHeader3", Value: "TestHeaderValue3"},
						{Name: "TestHeader4", Value: "TestHeaderValue4"},
					},
					"fe80::/64": {
						{Name: "TestHeader5", Value: "TestHeaderValue5"},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewDirectorSetHeadersByIP(tt.args.m)
			if (err != nil) != tt.wantErr {
				t.Fatal("NewDirectorSetHeadersByIP error", err)
			}

			if tt.wantErr {
				return
			}

			for _, ip := range tt.ips {
				err = got.IterByIncomingNetworks(ip, func(network net.IPNet, value HTTPHeaders) error {
					cidr := network.String()
					td.CmpDeeply(value, tt.args.m[cidr])
					delete(tt.args.m, cidr)
					return nil
				})
				td.CmpNoError(err)
			}

			if len(tt.args.m) > 0 {
				t.Fatalf("not all networks found, %v", tt.args.m)
			}
		})
	}
}

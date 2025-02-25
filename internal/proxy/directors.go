package proxy

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/rekby/lets-proxy2/internal/contextlabel"

	"github.com/rekby/lets-proxy2/internal/log"

	zc "github.com/rekby/zapcontext"

	"go.uber.org/zap"
)

const (
	ConnectionID = "{{CONNECTION_ID}}"
	HTTPProto    = "{{HTTP_PROTO}}"
	SourceIP     = "{{SOURCE_IP}}"
	SourcePort   = "{{SOURCE_PORT}}"
	SourceIPPort = "{{SOURCE_IP}}:{{SOURCE_PORT}}"
)

const (
	ProtocolHTTP  = "http"
	ProtocolHTTPS = "https"
)

type DirectorChain []Director

func (c DirectorChain) Director(request *http.Request) error {
	for _, d := range c {
		err := d.Director(request)
		if err != nil {
			return err
		}
	}
	return nil
}

// skip nil directors
func NewDirectorChain(directors ...Director) DirectorChain {
	cnt := 0

	for _, item := range directors {
		if item != nil {
			cnt++
		}
	}

	ownDirectors := make(DirectorChain, 0, cnt)

	for _, item := range directors {
		if item != nil {
			ownDirectors = append(ownDirectors, item)
		}
	}

	return ownDirectors
}

type DirectorSameIP struct {
	Port string
}

func NewDirectorSameIP(port int) DirectorSameIP {
	return DirectorSameIP{strconv.Itoa(port)}
}

func (s DirectorSameIP) Director(request *http.Request) error {
	localAddr := request.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if request.URL == nil {
		request.URL = &url.URL{}
	}
	request.URL.Host = localAddr.IP.String() + ":" + s.Port
	zc.L(request.Context()).Debug("Set target as same ip",
		zap.Stringer("local_addr", localAddr), zap.String("dest_host", request.Host))
	return nil
}

type DirectorDestMap map[string]string

func (d DirectorDestMap) Director(request *http.Request) error {
	ctx := request.Context()

	type Stringer interface {
		String() string
	}

	localAddr := ctx.Value(http.LocalAddrContextKey).(Stringer).String()
	var dest string
	var ok bool
	if dest, ok = d[localAddr]; !ok {
		zc.L(ctx).Debug("Map director no matches, skip.")
		return nil
	}

	if request.URL == nil {
		request.URL = &url.URL{}
	}
	request.URL.Host = dest
	zc.L(ctx).Debug("Map director set dest", zap.String("host", request.URL.Host))
	return nil
}

func NewDirectorDestMap(m map[string]string) DirectorDestMap {
	res := make(DirectorDestMap, len(m))
	for k, v := range m {
		res[k] = v
	}
	return res
}

type DirectorHost string

func (d DirectorHost) Director(request *http.Request) error {
	if request.URL == nil {
		request.URL = &url.URL{}
	}
	request.URL.Host = string(d)
	return nil
}

func NewDirectorHost(host string) DirectorHost {
	return DirectorHost(host)
}

type DirectorSetHeaders map[string]string

func NewDirectorSetHeaders(m map[string]string) DirectorSetHeaders {
	res := make(DirectorSetHeaders, len(m))
	for k, v := range m {
		res[k] = v
	}
	return res
}

func (h DirectorSetHeaders) Director(request *http.Request) error {
	ctx := request.Context()
	host, port, errHostPort := net.SplitHostPort(request.RemoteAddr)
	log.DebugDPanicCtx(ctx, errHostPort, "Parse remote addr for headers", zap.String("host", host), zap.String("port", port))

	for name, headerVal := range h {
		var value string

		switch headerVal {
		case ConnectionID:
			value = request.Context().Value(contextlabel.ConnectionID).(string)
		case HTTPProto:
			if tls, ok := ctx.Value(contextlabel.TLSConnection).(bool); ok {
				if tls {
					value = ProtocolHTTPS
				} else {
					value = ProtocolHTTP
				}
			} else {
				value = "error protocol detection"
			}
		case SourceIP:
			value = host
		case SourceIPPort:
			value = host + ":" + port
		case SourcePort:
			value = port
		default:
			value = headerVal
		}

		if request.Header == nil {
			request.Header = make(http.Header)
		}

		request.Header.Set(name, value)
	}
	return nil
}

type HTTPHeader struct {
	Name  string
	Value string
}
type HTTPHeaders []HTTPHeader
type NetHeaders struct {
	IPNet   net.IPNet
	Headers HTTPHeaders
}
type DirectorSetHeadersByIP []NetHeaders

func NewDirectorSetHeadersByIP(m map[string]HTTPHeaders) (DirectorSetHeadersByIP, error) {
	res := make(DirectorSetHeadersByIP, 0, len(m))
	for k, v := range m {
		_, subnet, err := net.ParseCIDR(k)
		if err != nil {
			return nil, fmt.Errorf("can't parse CIDR: %v %w", k, err)
		}

		res = append(res, NetHeaders{
			IPNet:   *subnet,
			Headers: v,
		})
	}
	sortByIPNet(res)
	return res, nil
}

func sortByIPNet(d DirectorSetHeadersByIP) {
	sort.Slice(d, func(i, j int) bool {
		left, right := d[i], d[j]

		maskOnes := func(m net.IPMask) int {
			ones, _ := m.Size()
			return ones
		}

		switch {
		case len(left.IPNet.IP) < len(right.IPNet.IP):
			return true
		case len(left.IPNet.IP) > len(right.IPNet.IP):
			return false
		case maskOnes(left.IPNet.Mask) < maskOnes(right.IPNet.Mask):
			return true
		case maskOnes(left.IPNet.Mask) > maskOnes(right.IPNet.Mask):
			return false
		default:
			return bytes.Compare(left.IPNet.IP, right.IPNet.IP) < 0
		}
	})
}

func (h DirectorSetHeadersByIP) Director(request *http.Request) error {
	if request == nil {
		return fmt.Errorf("request is nil")
	}

	ctx := request.Context()
	host, port, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		zc.L(ctx).Debug("Split host port error", zap.Error(err), zap.String("host", host),
			zap.String("port", port))
	}

	ip := net.ParseIP(host)

	for _, ipHeaders := range h {
		if !ipHeaders.IPNet.Contains(ip) {
			continue
		}

		if request.Header == nil {
			request.Header = make(http.Header)
		}

		for _, header := range ipHeaders.Headers {
			request.Header.Set(header.Name, header.Value)
		}
	}

	return nil
}

type DirectorSetScheme string

func (d DirectorSetScheme) Director(req *http.Request) error {
	if req.URL == nil {
		req.URL = &url.URL{}
	}
	req.URL.Scheme = string(d)
	return nil
}

func NewSetSchemeDirector(scheme string) DirectorSetScheme {
	return DirectorSetScheme(scheme)
}

package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/xjasonlyu/tun2socks/common/adapter"
	"github.com/xjasonlyu/tun2socks/component/dialer"
	obfs "github.com/xjasonlyu/tun2socks/component/simple-obfs"
	"github.com/xjasonlyu/tun2socks/component/socks5"

	"github.com/Dreamacro/go-shadowsocks2/core"
)

var _ Proxy = (*Shadowsocks)(nil)

type Shadowsocks struct {
	*Base

	cipher core.Cipher

	// simple-obfs plugin
	obfsMode, obfsHost string
}

func NewShadowsocks(addr, method, password, obfsMode, obfsHost string) (*Shadowsocks, error) {
	cipher, err := core.PickCipher(method, nil, password)
	if err != nil {
		return nil, fmt.Errorf("ss initialize: %w", err)
	}

	return &Shadowsocks{
		Base:     NewBase(addr),
		cipher:   cipher,
		obfsMode: obfsMode,
		obfsHost: obfsHost,
	}, nil
}

func (ss *Shadowsocks) Proto() string {
	return ShadowsocksProto.String()
}

func (ss *Shadowsocks) DialContext(ctx context.Context, metadata *adapter.Metadata) (c net.Conn, err error) {
	c, err = dialer.DialContext(ctx, "tcp", ss.Addr())
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", ss.Addr(), err)
	}
	setKeepAlive(c)

	defer func() {
		if err != nil && c != nil {
			c.Close()
		}
	}()

	switch ss.obfsMode {
	case "tls":
		c = obfs.NewTLSObfs(c, ss.obfsHost)
	case "http":
		_, port, _ := net.SplitHostPort(ss.addr)
		c = obfs.NewHTTPObfs(c, ss.obfsHost, port)
	}

	c = ss.cipher.StreamConn(c)
	_, err = c.Write(metadata.SerializesSocksAddr())
	return
}

func (ss *Shadowsocks) DialUDP(_ *adapter.Metadata) (net.PacketConn, error) {
	pc, err := dialer.ListenPacket("udp", "")
	if err != nil {
		return nil, fmt.Errorf("listen packet: %w", err)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", ss.Addr())
	if err != nil {
		return nil, fmt.Errorf("resolve udp address %s: %w", ss.Addr(), err)
	}

	pc = ss.cipher.PacketConn(pc)
	return &ssPacketConn{PacketConn: pc, rAddr: udpAddr}, nil
}

type ssPacketConn struct {
	net.PacketConn

	rAddr net.Addr
}

func (pc *ssPacketConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	var packet []byte
	if m, ok := addr.(*adapter.Metadata); ok {
		packet, err = socks5.EncodeUDPPacket(m.SerializesSocksAddr(), b)
	} else {
		packet, err = socks5.EncodeUDPPacket(socks5.ParseAddrToSocksAddr(addr), b)
	}

	if err != nil {
		return
	}
	return pc.PacketConn.WriteTo(packet[3:], pc.rAddr)
}

func (pc *ssPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, _, err := pc.PacketConn.ReadFrom(b)
	if err != nil {
		return 0, nil, err
	}

	addr := socks5.SplitAddr(b[:n])
	if addr == nil {
		return 0, nil, errors.New("parse addr error")
	}

	udpAddr := addr.UDPAddr()
	if udpAddr == nil {
		return 0, nil, errors.New("parse addr error")
	}

	copy(b, b[len(addr):])
	return n - len(addr), udpAddr, err
}

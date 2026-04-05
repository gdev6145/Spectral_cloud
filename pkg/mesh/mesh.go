package mesh

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	NodeID            uint32
	BindAddr          string
	Peers             []string
	HeartbeatInterval time.Duration
	RouteTTL          time.Duration
	SharedKeys        []string
	PeerKeys          map[string]string
	TenantID          string
}

type Node struct {
	cfg    Config
	router *routing.RoutingEngine

	conn   *net.UDPConn
	stats  *Stats
	mu     sync.Mutex
	closed bool
}

type Stats struct {
	Received      uint64
	Accepted      uint64
	Rejected      uint64
	RejectedAuth  uint64
	RejectedParse uint64
}

func Start(ctx context.Context, cfg Config, router *routing.RoutingEngine) (*Node, error) {
	if router == nil {
		return nil, errors.New("router is required")
	}
	if cfg.BindAddr == "" {
		return nil, errors.New("bind addr is required")
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.RouteTTL <= 0 {
		cfg.RouteTTL = 30 * time.Second
	}

	addr, err := net.ResolveUDPAddr("udp", cfg.BindAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	node := &Node{
		cfg:    cfg,
		router: router,
		conn:   conn,
		stats:  &Stats{},
	}

	go node.readLoop(ctx)
	go node.heartbeatLoop(ctx)
	go node.handshakeOnce(ctx)

	return node, nil
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	n.closed = true
	return n.conn.Close()
}

func (n *Node) Snapshot() (Stats, Config) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return *n.stats, n.cfg
}

func (n *Node) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.broadcastControl(meshpb.ControlMessage_HEARTBEAT)
		}
	}
}

func (n *Node) handshakeOnce(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
		n.broadcastControl(meshpb.ControlMessage_HANDSHAKE)
	}
}

func (n *Node) broadcastControl(control meshpb.ControlMessage_ControlType) {
	payloadBytes, _ := marshalControlPayload(controlPayload{
		Timestamp: time.Now().UTC().UnixMilli(),
		Addr:      n.cfg.BindAddr,
	}, n.sharedKeyFor(""))
	msg := &meshpb.ControlMessage{
		MsgType:     meshpb.DataMessage_DATA,
		ControlType: control,
		NodeId:      n.cfg.NodeID,
		Payload:     payloadBytes,
		TenantId:    n.cfg.TenantID,
	}
	for _, peer := range n.cfg.Peers {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		key := n.sharedKeyFor(peer)
		payloadBytes, _ := marshalControlPayload(controlPayload{
			Timestamp: time.Now().UTC().UnixMilli(),
			Addr:      n.cfg.BindAddr,
		}, key)
		msg.Payload = payloadBytes
		wire, err := proto.Marshal(msg)
		if err != nil {
			continue
		}
		addr, err := net.ResolveUDPAddr("udp", peer)
		if err != nil {
			continue
		}
		_, _ = n.conn.WriteToUDP(wire, addr)
	}
}

func (n *Node) readLoop(ctx context.Context) {
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		nn, addr, err := n.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		n.handlePacket(buf[:nn], addr)
	}
}

func (n *Node) handlePacket(data []byte, addr *net.UDPAddr) {
	n.mu.Lock()
	n.stats.Received++
	n.mu.Unlock()

	var msg meshpb.ControlMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		n.mu.Lock()
		n.stats.Rejected++
		n.stats.RejectedParse++
		n.mu.Unlock()
		return
	}
	if msg.ControlType != meshpb.ControlMessage_HEARTBEAT && msg.ControlType != meshpb.ControlMessage_HANDSHAKE {
		n.mu.Lock()
		n.stats.Rejected++
		n.stats.RejectedParse++
		n.mu.Unlock()
		return
	}
	if strings.TrimSpace(n.cfg.TenantID) != "" && msg.TenantId != "" && msg.TenantId != n.cfg.TenantID {
		n.mu.Lock()
		n.stats.Rejected++
		n.stats.RejectedAuth++
		n.mu.Unlock()
		return
	}
	var payload controlPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		n.mu.Lock()
		n.stats.Rejected++
		n.stats.RejectedParse++
		n.mu.Unlock()
		return
	}
	if !verifyControlPayload(payload, n.keyCandidates(addr, payload.Addr)) {
		n.mu.Lock()
		n.stats.Rejected++
		n.stats.RejectedAuth++
		n.mu.Unlock()
		return
	}
	latency := 0
	if payload.Timestamp > 0 {
		delta := time.Now().UTC().UnixMilli() - payload.Timestamp
		if delta > 0 && delta < int64(time.Hour/time.Millisecond) {
			latency = int(delta)
		}
	}
	routeAddr := addr.String()
	if payload.Addr != "" {
		routeAddr = payload.Addr
	}
	_ = n.router.AddRouteWithTTL(routeAddr, routing.RouteMetric{
		Latency:    latency,
		Throughput: 0,
	}, n.cfg.RouteTTL)

	n.mu.Lock()
	n.stats.Accepted++
	n.mu.Unlock()
}

type controlPayload struct {
	Timestamp int64  `json:"ts"`
	Addr      string `json:"addr"`
	Signature string `json:"sig,omitempty"`
}

func marshalControlPayload(payload controlPayload, key string) ([]byte, error) {
	if strings.TrimSpace(key) == "" {
		return json.Marshal(payload)
	}
	payload.Signature = ""
	base, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sig := sign(base, key)
	payload.Signature = sig
	return json.Marshal(payload)
}

func verifyControlPayload(payload controlPayload, keys []string) bool {
	if len(keys) == 0 {
		return true
	}
	if strings.TrimSpace(payload.Signature) == "" {
		return false
	}
	sig := payload.Signature
	payload.Signature = ""
	base, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		expected := sign(base, key)
		if secureEquals(sig, expected) {
			return true
		}
	}
	return false
}

func sign(data []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(data)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func secureEquals(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var out byte
	for i := 0; i < len(a); i++ {
		out |= a[i] ^ b[i]
	}
	return out == 0
}

func (n *Node) sharedKeyFor(peer string) string {
	if strings.TrimSpace(peer) != "" {
		if key, ok := n.cfg.PeerKeys[peer]; ok && strings.TrimSpace(key) != "" {
			return key
		}
	}
	if len(n.cfg.SharedKeys) > 0 {
		return n.cfg.SharedKeys[0]
	}
	return ""
}

func (n *Node) keyCandidates(addr *net.UDPAddr, claimedAddr string) []string {
	var keys []string
	if claimedAddr != "" {
		if key, ok := n.cfg.PeerKeys[claimedAddr]; ok {
			keys = append(keys, key)
		}
	}
	if addr != nil {
		if key, ok := n.cfg.PeerKeys[addr.String()]; ok {
			keys = append(keys, key)
		}
	}
	keys = append(keys, n.cfg.SharedKeys...)
	return keys
}

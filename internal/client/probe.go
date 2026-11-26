package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Tunnel probe constants. The responder listens on every active network's
// virtual IP, so a probe is answered by the peer's Snet daemon over the
// WireGuard tunnel instead of relying on ICMP, which host firewalls (and
// unprivileged containers) commonly block.
const (
	probePort     = 19433
	probeCount    = 5
	probeInterval = 90 * time.Millisecond
	probeWait     = 1200 * time.Millisecond
)

var probeMagic = []byte("SNETPRB1")

type probeResponder struct {
	conn      *net.UDPConn
	done      chan struct{}
	closeOnce sync.Once
}

// startProbeResponder binds the echo listener on ip:probePort. A failure is
// logged and ignored: the network stays usable, but peers cannot test this
// device until the listener can bind.
func startProbeResponder(ip string) *probeResponder {
	return startProbeResponderAt(ip, probePort)
}

func startProbeResponderAt(ip string, port int) *probeResponder {
	addr := &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("probe responder %s: %v", ip, err)
		return nil
	}
	r := &probeResponder{conn: conn, done: make(chan struct{})}
	go r.loop()
	return r
}

func (r *probeResponder) loop() {
	buf := make([]byte, 64)
	for {
		n, addr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.done:
				return
			default:
				continue
			}
		}
		if n < len(probeMagic) || !bytes.Equal(buf[:len(probeMagic)], probeMagic) {
			continue
		}
		_, _ = r.conn.WriteToUDP(buf[:n], addr)
	}
}

func (r *probeResponder) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		close(r.done)
		_ = r.conn.Close()
	})
}

// probeUDP sends count echo probes to target:port from localIP and reports
// loss percent plus average RTT in milliseconds.
func probeUDP(localIP, targetIP net.IP, port, count int) (loss, avgRtt float64, sent, received int, err error) {
	if count <= 0 {
		count = probeCount
	}
	laddr := &net.UDPAddr{IP: localIP}
	raddr := &net.UDPAddr{IP: targetIP, Port: port}
	conn, err := net.DialUDP("udp", laddr, raddr)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer conn.Close()

	var mu sync.Mutex
	pending := make(map[uint32]time.Time, count)
	var rtts []float64

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 64)
		got := 0
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			if n < 12 || !bytes.Equal(buf[:len(probeMagic)], probeMagic) {
				continue
			}
			seq := binary.BigEndian.Uint32(buf[8:12])
			mu.Lock()
			at, ok := pending[seq]
			if ok {
				delete(pending, seq)
				rtts = append(rtts, float64(time.Since(at).Microseconds())/1000)
				got++
			}
			mu.Unlock()
			if ok && got >= count {
				return
			}
		}
	}()

	payload := make([]byte, 12)
	copy(payload, probeMagic)
	for i := 0; i < count; i++ {
		seq := uint32(i + 1)
		binary.BigEndian.PutUint32(payload[8:], seq)
		sent++
		mu.Lock()
		pending[seq] = time.Now()
		mu.Unlock()
		if _, werr := conn.Write(payload); werr != nil {
			mu.Lock()
			delete(pending, seq)
			mu.Unlock()
		}
		if i < count-1 {
			time.Sleep(probeInterval)
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(probeWait))
	<-readDone

	mu.Lock()
	received = len(rtts)
	for _, rtt := range rtts {
		avgRtt += rtt
	}
	mu.Unlock()
	if received > 0 {
		avgRtt /= float64(received)
	}
	if sent > 0 {
		loss = float64(sent-received) / float64(sent) * 100
	}
	return loss, avgRtt, sent, received, nil
}

// Probe tests reachability and response quality to another member of nid.
// It uses the daemon's own tunnel echo first and falls back to ICMP so peers
// still running an older client can be tested.
func (d *Daemon) Probe(nid, target string) (map[string]any, error) {
	targetIP := net.ParseIP(strings.TrimSpace(target))
	if targetIP == nil {
		return nil, errors.New("目标 IP 无效")
	}
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("网络 %s 未找到", nid)
	}
	if !nc.Active || d.nets[nid] == nil {
		d.mu.Unlock()
		return nil, errors.New("网络未连接，无法测试")
	}
	selfIP := net.ParseIP(nc.IP)
	subnet := nc.Subnet
	d.mu.Unlock()
	if selfIP == nil {
		return nil, errors.New("本机虚拟 IP 无效")
	}
	if targetIP.Equal(selfIP) {
		return map[string]any{
			"loss": 0.0, "avgRtt": 0.0, "sent": 0, "received": 0, "method": "self",
		}, nil
	}
	if _, netCIDR, cerr := net.ParseCIDR(subnet); cerr == nil && !netCIDR.Contains(targetIP) {
		return nil, errors.New("目标设备不属于当前网络")
	}

	loss, avg, sent, received, err := probeUDP(selfIP, targetIP, probePort, probeCount)
	if err != nil {
		return nil, err
	}
	method := "probe"
	if received == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
		defer cancel()
		if ploss, pavg, perr := icmpPing(ctx, target); perr == nil && ploss < 100 {
			loss, avg, method = ploss, pavg, "icmp"
		}
	}
	return map[string]any{
		"loss": loss, "avgRtt": avg, "sent": sent, "received": received, "method": method,
	}, nil
}

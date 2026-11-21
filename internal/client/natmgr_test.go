package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeUpnp struct {
	externalIP string
	addErr     error
	ipErr      error
	delErr     error
	deleted    bool
}

func (f *fakeUpnp) AddPortMappingCtx(context.Context, string, uint16, string, uint16, string, bool, string, uint32) error {
	return f.addErr
}

func (f *fakeUpnp) GetExternalIPAddressCtx(context.Context) (string, error) {
	return f.externalIP, f.ipErr
}

func (f *fakeUpnp) DeletePortMappingCtx(context.Context, string, uint16, string) error {
	f.deleted = true
	return f.delErr
}

func TestParseNatPMPPublic(t *testing.T) {
	good := []byte{
		0, 0x80, // version, opcode
		0, 0, // result: success
		0, 0, 0, 1, // epoch
		203, 0, 113, 4, // external IP 203.0.113.4
	}
	ip, err := parseNatPMPPublic(good)
	if err != nil || ip != "203.0.113.4" {
		t.Fatalf("parseNatPMPPublic(good) = %q, %v", ip, err)
	}
	if _, err := parseNatPMPPublic(good[:8]); err == nil {
		t.Fatal("short reply should be rejected")
	}
	refused := []byte{0, 0x80, 0, 2, 0, 0, 0, 1, 1, 2, 3, 4}
	if _, err := parseNatPMPPublic(refused); err == nil || err.Error() != "nat-pmp refused (not-authorized)" {
		t.Fatalf("refused reply should be rejected, got %v", err)
	}
}

func TestParseNatPMPMap(t *testing.T) {
	good := []byte{
		0, 0x82, // version, opcode map-udp
		0, 0, // result: success
		0, 0, 0, 1, // epoch
		0xca, 0x6c, // internal port 51820
		0x9c, 0x40, // external port 40000
		0, 0, 1, 0x2c, // lifetime 300
	}
	port, lease, err := parseNatPMPMap(good)
	if err != nil || port != 40000 || lease != 300 {
		t.Fatalf("parseNatPMPMap(good) = %d, %d, %v", port, lease, err)
	}
	for _, n := range []int{10, 15} {
		if _, _, err := parseNatPMPMap(good[:n]); err == nil {
			t.Fatalf("%d-byte map reply should be rejected", n)
		}
	}
	bad := append([]byte{}, good...)
	binary.BigEndian.PutUint16(bad[2:4], 3) // network-failure
	if _, _, err := parseNatPMPMap(bad); err == nil {
		t.Fatal("refused map reply should be rejected")
	}
}

func TestParsePCPMapResp(t *testing.T) {
	resp := make([]byte, 60)
	resp[0] = 2                                    // version
	resp[1] = 1<<7 | 1                             // R=1, opcode MAP
	resp[3] = 0                                    // result: success
	binary.BigEndian.PutUint32(resp[4:8], 300)     // lifetime
	resp[36] = 17                                  // IPPROTO_UDP
	binary.BigEndian.PutUint16(resp[40:42], 51820) // internal port
	// Assigned external port and IP live at 42:44 and 44:60.
	binary.BigEndian.PutUint16(resp[42:44], 40001)
	// Assigned external IP is encoded IPv4-mapped: ::ffff:203.0.113.9.
	copy(resp[44:60], []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 203, 0, 113, 9})
	ip, port, lease, err := parsePCPMapResp(resp)
	if err != nil || ip != "203.0.113.9" || port != 40001 || lease != 300 {
		t.Fatalf("parsePCPMapResp(good) = %q, %d, %d, %v", ip, port, lease, err)
	}
	refused := append([]byte{}, resp...)
	refused[3] = 5 // unsupported-option
	if _, _, _, err := parsePCPMapResp(refused); err == nil || err.Error() != "pcp refused (unsupported-option)" {
		t.Fatalf("refused pcp reply should report unsupported-option, got %v", err)
	}
	noIP := append([]byte{}, resp...)
	copy(noIP[44:60], make([]byte, 16))
	if _, _, _, err := parsePCPMapResp(noIP); err == nil {
		t.Fatal("pcp reply without assigned IP should be rejected")
	}
	for _, n := range []int{52, 59} {
		if _, _, _, err := parsePCPMapResp(resp[:n]); err == nil {
			t.Fatalf("%d-byte pcp reply should be rejected", n)
		}
	}
}

func TestPCPMapReqShape(t *testing.T) {
	var nonce [12]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	req, err := pcpMapReq(51820, nonce, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if len(req) != 60 {
		t.Fatalf("pcp request len = %d, want 60", len(req))
	}
	if req[0] != 2 || req[1] != 1 {
		t.Fatalf("pcp header version/opcode = %d/%d", req[0], req[1])
	}
	if !bytes.Equal(req[8:24], net.ParseIP("192.0.2.10").To16()) {
		t.Fatalf("pcp client IP = %x, want v4-mapped 192.0.2.10", req[8:24])
	}
	if req[36] != 17 {
		t.Fatalf("pcp protocol = %d, want 17 (UDP)", req[36])
	}
	if binary.BigEndian.Uint16(req[40:42]) != 51820 || binary.BigEndian.Uint16(req[42:44]) != 51820 {
		t.Fatalf("pcp internal/external ports wrong: %v", req[40:44])
	}
	if _, err := pcpMapReq(51820, nonce, "not-an-ip"); err == nil {
		t.Fatal("invalid client IP should be rejected")
	}
}

func TestSameHost(t *testing.T) {
	cases := []struct {
		ep   string
		host string
		want bool
	}{
		{"39.180.139.102:51820", "39.180.139.102", true},
		{"39.180.139.102:51820", "66.187.6.46", false},
		{"39.180.139.102", "39.180.139.102", true},
		{"[2001:db8::1]:51820", "2001:db8::1", true},
		{"", "39.180.139.102", false},
	}
	for _, c := range cases {
		if got := sameHost(c.ep, c.host); got != c.want {
			t.Fatalf("sameHost(%q, %q) = %v, want %v", c.ep, c.host, got, c.want)
		}
	}
}

func TestMapUpnp(t *testing.T) {
	ok := &fakeUpnp{externalIP: "203.0.113.7"}
	res, err := mapUpnp(context.Background(), ok, "http://192.168.1.1/rootDesc.xml", "ig2", 51820, "192.168.1.5")
	if err != nil || res.externalIP != "203.0.113.7" || res.externalPort != 51820 || res.via != "upnp" {
		t.Fatalf("mapUpnp(ok) = %+v, %v", res, err)
	}
	if ok.deleted {
		t.Fatal("successful mapping must not be deleted")
	}

	badIP := &fakeUpnp{externalIP: "not-an-ip"}
	if _, err := mapUpnp(context.Background(), badIP, "http://192.168.1.1/rootDesc.xml", "ig2", 51820, "192.168.1.5"); err == nil {
		t.Fatal("non-IP external address should be rejected")
	}
	if !badIP.deleted {
		t.Fatal("unverifiable mapping should be removed")
	}

	refused := &fakeUpnp{addErr: errors.New("refused")}
	if _, err := mapUpnp(context.Background(), refused, "http://192.168.1.1/rootDesc.xml", "ig2", 51820, "192.168.1.5"); err == nil {
		t.Fatal("AddPortMapping failure should be returned")
	}
	if refused.deleted {
		t.Fatal("failed AddPortMapping should not attempt deletion")
	}
}

// TestNatManagerEnabled verifies the nil/true/false configuration contract and
// runtime toggling.
func TestNatManagerEnabled(t *testing.T) {
	cfg := &Config{}
	n := newNatManager(cfg, context.Background())
	if !n.enabled() {
		t.Fatal("nil NATMapping should default to enabled")
	}
	n.setEnabled(false)
	if n.enabled() {
		t.Fatal("setEnabled(false) should disable mappings")
	}
	n.setEnabled(true)
	if !n.enabled() {
		t.Fatal("setEnabled(true) should enable mappings")
	}

	off := false
	n = newNatManager(&Config{NATMapping: &off}, context.Background())
	if n.enabled() {
		t.Fatal("NATMapping=false should start disabled")
	}
}

func TestStoreMappingOnlyWhileClaimed(t *testing.T) {
	cfg := &Config{WireguardPort: 51820}
	n := newNatManager(cfg, context.Background())
	res := natResult{externalIP: "203.0.113.1", externalPort: 51820, via: "upnp"}

	if _, stored := n.storeMapping(51820, res); stored {
		t.Fatal("unclaimed port must not store a mapping")
	}
	n.claim(51820, "https://test.example:8090")
	if unchanged, stored := n.storeMapping(51820, res); !stored || unchanged {
		t.Fatalf("first store = unchanged %v, stored %v; want false, true", unchanged, stored)
	}
	if unchanged, stored := n.storeMapping(51820, res); !stored || !unchanged {
		t.Fatalf("repeat store = unchanged %v, stored %v; want true, true", unchanged, stored)
	}
	n.unclaim(51820)
	if _, stored := n.storeMapping(51820, res); stored {
		t.Fatal("mapping completing after unclaim must be discarded")
	}
}

func TestNatManagerBookkeeping(t *testing.T) {
	cfg := &Config{WireguardPort: 51820}
	n := newNatManager(cfg, context.Background())
	n.claim(51820, "https://test.example:8090")
	n.claim(51821, "https://test.example:8090")
	if _, ok := n.mapping(51820); ok {
		t.Fatal("no mapping should exist before a successful sync")
	}
	n.setPort(51820, natResult{externalIP: "203.0.113.1", externalPort: 51820, via: "upnp"}, true)
	res, ok := n.mapping(51820)
	if !ok || res.externalPort != 51820 || res.via != "upnp" {
		t.Fatalf("mapping(51820) = %+v, %v", res, ok)
	}
	if n.setPort(51820, res, true) != true {
		t.Fatal("re-setting the same mapping should report unchanged")
	}
	n.unclaim(51820)
	if _, ok := n.mapping(51820); ok {
		t.Fatal("unclaim should clear the mapping")
	}
}

func TestNatManagerStatusAndErrorThrottle(t *testing.T) {
	cfg := &Config{WireguardPort: 51820}
	n := newNatManager(cfg, context.Background())
	n.claim(51821, "https://test.example:8090")
	n.setPort(51820, natResult{
		externalIP:   "203.0.113.1",
		externalPort: 51820,
		via:          "upnp",
		lease:        natMapLeaseSec,
	}, true)

	err := errors.New("no gateway")
	if !n.noteError(51821, err) {
		t.Fatal("first failure should be logged")
	}
	if n.noteError(51821, err) {
		t.Fatal("repeated failure inside the throttle window should be suppressed")
	}
	if n.noteError(51821, errors.New("different failure")) != true {
		t.Fatal("a changed failure should be logged immediately")
	}
	n.noteOK(51821)

	st := n.status()
	enabled, _ := st["enabled"].(bool)
	if !enabled {
		t.Fatal("status enabled = false, want true")
	}
	mappings, _ := st["mappings"].([]map[string]any)
	if len(mappings) != 1 || mappings[0]["port"] != 51820 || mappings[0]["via"] != "upnp" {
		t.Fatalf("status mappings = %+v", mappings)
	}
	errs, _ := st["errors"].([]map[string]any)
	if len(errs) != 0 {
		t.Fatalf("status errors = %+v, want empty after success", errs)
	}

	n.setEnabled(false)
	if _, ok := n.mapping(51820); ok {
		t.Fatal("disabling should clear mappings")
	}
}

func TestNatPMPMapExchange(t *testing.T) {
	addr, stop := startUDPServer(t, func(conn *net.UDPConn) {
		buf := make([]byte, 512)
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n != 2 || buf[0] != 0 || buf[1] != 0 {
			t.Errorf("public-address request = %x", buf[:n])
			return
		}
		_, _ = conn.WriteToUDP([]byte{0, 0x80, 0, 0, 0, 0, 0, 1, 203, 0, 113, 4}, peer)

		n, peer, err = conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n != 12 || buf[1] != 2 || binary.BigEndian.Uint16(buf[4:6]) != 51820 {
			t.Errorf("map request = %x", buf[:n])
			return
		}
		resp := []byte{0, 0x82, 0, 0, 0, 0, 0, 1, 0xca, 0x6c, 0x9c, 0x40, 0, 0, 1, 0x2c}
		_, _ = conn.WriteToUDP(resp, peer)
	})
	defer stop()

	res, err := natpmpMapAt(context.Background(), 51820, "192.168.1.5", "127.0.0.1", addr)
	if err != nil {
		t.Fatal(err)
	}
	if res.externalIP != "203.0.113.4" || res.externalPort != 40000 || res.lease != 300 || res.via != "natpmp" {
		t.Fatalf("natpmp result = %+v", res)
	}
}

func TestPCPMapExchange(t *testing.T) {
	addr, stop := startUDPServer(t, func(conn *net.UDPConn) {
		buf := make([]byte, 512)
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n != 60 || buf[0] != 2 || buf[1] != 1 || buf[36] != 17 {
			t.Errorf("pcp request header = %x", buf[:n])
			return
		}
		resp := make([]byte, 60)
		resp[0] = 2
		resp[1] = 1<<7 | 1
		binary.BigEndian.PutUint32(resp[4:8], 300)
		copy(resp[24:36], buf[24:36])
		binary.BigEndian.PutUint16(resp[40:42], 51820)
		binary.BigEndian.PutUint16(resp[42:44], 40002)
		copy(resp[44:60], net.ParseIP("203.0.113.9").To16())
		_, _ = conn.WriteToUDP(resp, peer)
	})
	defer stop()

	res, err := pcpMapAt(context.Background(), 51820, "192.168.1.5", "127.0.0.1", addr)
	if err != nil {
		t.Fatal(err)
	}
	if res.externalIP != "203.0.113.9" || res.externalPort != 40002 || res.via != "pcp" {
		t.Fatalf("pcp result = %+v", res)
	}
}

func TestPCPMapRejectsNonceMismatch(t *testing.T) {
	addr, stop := startUDPServer(t, func(conn *net.UDPConn) {
		buf := make([]byte, 512)
		_, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		resp := make([]byte, 60)
		resp[0] = 2
		resp[1] = 1<<7 | 1
		binary.BigEndian.PutUint16(resp[42:44], 40002)
		copy(resp[44:60], net.ParseIP("203.0.113.9").To16())
		_, _ = conn.WriteToUDP(resp, peer)
	})
	defer stop()

	if _, err := pcpMapAt(context.Background(), 51820, "192.168.1.5", "127.0.0.1", addr); err == nil {
		t.Fatal("mismatched PCP nonce should be rejected")
	}
}

func startUDPServer(t *testing.T, handle func(*net.UDPConn)) (string, func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handle(conn)
	}()
	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}

func TestDaemonSetNATMappingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	d := NewDaemonAt(&Config{WireguardPort: 51820}, path)
	if !d.nat.enabled() {
		t.Fatal("default daemon should have NAT mapping enabled")
	}
	if err := d.SetNATMapping(false); err != nil {
		t.Fatal(err)
	}
	if d.nat.enabled() {
		t.Fatal("daemon should disable NAT mapping at runtime")
	}
	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NATMapping == nil || *cfg.NATMapping {
		t.Fatalf("persisted NATMapping = %v, want false", cfg.NATMapping)
	}
	if err := d.SetNATMapping(true); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NATMapping == nil || !*cfg.NATMapping {
		t.Fatalf("persisted NATMapping = %v, want true", cfg.NATMapping)
	}
}

func TestMappedEndpointFor(t *testing.T) {
	const port = 51820
	d, _ := newTestDaemon(t)
	d.nat.setPort(port, natResult{
		externalIP:   "203.0.113.10",
		externalPort: 51820,
		via:          "upnp",
	}, true)

	t.Run("matching WAN", func(t *testing.T) {
		if got := d.mappedEndpointFor(port, "203.0.113.10"); got != "203.0.113.10:51820" {
			t.Fatalf("mappedEndpointFor = %q, want %q", got, "203.0.113.10:51820")
		}
	})
	t.Run("mismatched WAN", func(t *testing.T) {
		if got := d.mappedEndpointFor(port, "198.51.100.7"); got != "" {
			t.Fatalf("mappedEndpointFor = %q, want empty", got)
		}
	})
	t.Run("no mapping", func(t *testing.T) {
		if got := d.mappedEndpointFor(51821, "203.0.113.10"); got != "" {
			t.Fatalf("mappedEndpointFor = %q, want empty", got)
		}
	})
	t.Run("empty observed IP", func(t *testing.T) {
		if got := d.mappedEndpointFor(port, ""); got != "" {
			t.Fatalf("mappedEndpointFor = %q, want empty", got)
		}
	})
}

type fakeUPnPService struct {
	server        *httptest.Server
	mu            sync.Mutex
	actions       []string
	deletedPorts  []int
	deleteStarted chan struct{}
	releaseDelete chan struct{}
}

func newFakeUPnPService(t *testing.T, blockDelete bool) *fakeUPnPService {
	t.Helper()
	f := &fakeUPnPService{}
	if blockDelete {
		f.deleteStarted = make(chan struct{}, 1)
		f.releaseDelete = make(chan struct{})
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeUPnPService) url() string { return f.server.URL + "/rootDesc.xml" }

func (f *fakeUPnPService) actionCount(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.actions {
		if a == action {
			n++
		}
	}
	return n
}

func (f *fakeUPnPService) deleted() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.deletedPorts...)
}

func (f *fakeUPnPService) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/rootDesc.xml":
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, fakeUPnPRootDesc)
		return
	case "/control":
		body, _ := io.ReadAll(r.Body)
		action := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
		if i := strings.LastIndex(action, "#"); i >= 0 {
			action = action[i+1:]
		}
		f.mu.Lock()
		f.actions = append(f.actions, action)
		if action == "DeletePortMapping" {
			if p := soapPort(body, "NewExternalPort"); p != 0 {
				f.deletedPorts = append(f.deletedPorts, p)
			}
		}
		started := f.deleteStarted
		release := f.releaseDelete
		f.mu.Unlock()
		if action == "DeletePortMapping" && started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
			if release != nil {
				<-release
			}
		}
		w.Header().Set("Content-Type", "text/xml")
		if action == "GetExternalIPAddress" {
			_, _ = io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:2"><NewExternalIPAddress>203.0.113.10</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`)
			return
		}
		_, _ = io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:`+action+`Response xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:2"/></s:Body></s:Envelope>`)
		return
	default:
		http.NotFound(w, r)
	}
}

func soapPort(body []byte, tag string) int {
	s := string(body)
	start := strings.Index(s, "<"+tag+">")
	if start < 0 {
		return 0
	}
	start += len(tag) + 2
	end := strings.Index(s[start:], "</"+tag+">")
	if end < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s[start : start+end]))
	return n
}

const fakeUPnPRootDesc = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:2</deviceType>
    <friendlyName>fake</friendlyName>
    <manufacturer>fake</manufacturer>
    <UDN>uuid:fake</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:WANIPConnection:2</serviceType>
        <serviceId>urn:upnp-org:serviceId:WANIPConnection2</serviceId>
        <SCPDURL>/scpd.xml</SCPDURL>
        <controlURL>/control</controlURL>
        <eventSubURL>/event</eventSubURL>
      </service>
    </serviceList>
  </device>
</root>`

func TestUnclaimTearsDownUPnPMapping(t *testing.T) {
	f := newFakeUPnPService(t, false)
	n := newNatManager(&Config{}, context.Background())
	const port = 51820
	n.claim(port, "https://test.example:8090")
	n.setPort(port, natResult{
		externalIP:   "203.0.113.10",
		externalPort: port,
		via:          "upnp",
		upnpURL:      f.url(),
		upnpKind:     "ig2",
	}, true)

	n.unclaim(port)
	n.waitPending(port)

	if got := f.actionCount("DeletePortMapping"); got != 1 {
		t.Fatalf("DeletePortMapping calls = %d, want 1", got)
	}
	if got := f.deleted(); len(got) != 1 || got[0] != port {
		t.Fatalf("deleted ports = %v, want [%d]", got, port)
	}
}

func TestUnclaimSkipsTeardownForNatPMPAndPCP(t *testing.T) {
	f := newFakeUPnPService(t, false)
	n := newNatManager(&Config{}, context.Background())
	for _, via := range []string{"natpmp", "pcp"} {
		const port = 51820
		n.claim(port, "https://test.example:8090")
		n.setPort(port, natResult{
			externalIP:   "203.0.113.10",
			externalPort: port,
			via:          via,
			upnpURL:      f.url(),
			upnpKind:     "ig2",
		}, true)
		n.unclaim(port)
		n.waitPending(port)
	}
	if got := f.actionCount("DeletePortMapping"); got != 0 {
		t.Fatalf("NAT-PMP/PCP unclaim issued %d DeletePortMapping calls, want 0", got)
	}
}

func TestReclaimWaitsForPendingTeardown(t *testing.T) {
	f := newFakeUPnPService(t, true)
	n := newNatManager(&Config{}, context.Background())
	const port = 51820
	n.claim(port, "https://test.example:8090")
	n.setPort(port, natResult{
		externalIP:   "203.0.113.10",
		externalPort: port,
		via:          "upnp",
		upnpURL:      f.url(),
		upnpKind:     "ig2",
	}, true)

	n.unclaim(port)
	select {
	case <-f.deleteStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("DeletePortMapping did not start")
	}

	n.claim(port, "https://test.example:8090")
	waitDone := make(chan struct{})
	go func() {
		n.waitPending(port)
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("waitPending returned before old teardown finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(f.releaseDelete)
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waitPending did not return after teardown finished")
	}

	newRes := natResult{
		externalIP:   "203.0.113.11",
		externalPort: 51821,
		via:          "upnp",
		upnpURL:      f.url(),
		upnpKind:     "ig2",
	}
	n.setPort(port, newRes, true)
	got, ok := n.mapping(port)
	if !ok || got != newRes {
		t.Fatalf("final mapping = %+v, %v; want new mapping %+v", got, ok, newRes)
	}
}

func startCtlServer(t *testing.T, d *Daemon, token string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	srvCh := make(chan *http.Server, 1)
	go func() {
		_ = ServeCtl(d, token, addr, func(s *http.Server) { srvCh <- s })
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ctl server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/ctl/shutdown", nil)
		req.Header.Set("X-Ctl-Token", token)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		select {
		case s := <-srvCh:
			_ = s.Close()
		case <-time.After(2 * time.Second):
		}
	})
	return addr
}

func TestCtlSetNATMappingTearsDownOnDisable(t *testing.T) {
	d, cfgPath := newTestDaemon(t)
	f := newFakeUPnPService(t, false)
	const port = 51820
	d.nat.claim(port, "https://test.example:8090")
	d.nat.setPort(port, natResult{
		externalIP:   "203.0.113.10",
		externalPort: port,
		via:          "upnp",
		upnpURL:      f.url(),
		upnpKind:     "ig2",
	}, true)

	addr := startCtlServer(t, d, "test-token")
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/ctl/nat", bytes.NewBufferString(`{"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Ctl-Token", "test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204 (body %s)", resp.StatusCode, body)
	}
	if got := f.actionCount("DeletePortMapping"); got != 1 {
		t.Fatalf("DeletePortMapping calls = %d, want 1", got)
	}
	if got := f.deleted(); len(got) != 1 || got[0] != port {
		t.Fatalf("deleted ports = %v, want [%d]", got, port)
	}
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NATMapping == nil || *cfg.NATMapping {
		t.Fatalf("persisted NATMapping = %v, want false", cfg.NATMapping)
	}
	cur := d.Config()
	if cur.NATMapping == nil || *cur.NATMapping {
		t.Fatalf("in-memory NATMapping = %v, want false", cur.NATMapping)
	}
}

func TestCtlSetNATMappingEnablePersistsWithoutTeardown(t *testing.T) {
	d, cfgPath := newTestDaemon(t)
	f := newFakeUPnPService(t, false)
	const port = 51820

	off := false
	d.mu.Lock()
	d.cfg.NATMapping = &off
	d.mu.Unlock()
	d.nat.setEnabled(false)
	d.nat.claim(port, "https://test.example:8090")
	d.nat.setPort(port, natResult{
		externalIP:   "203.0.113.10",
		externalPort: port,
		via:          "upnp",
		upnpURL:      f.url(),
		upnpKind:     "ig2",
	}, true)

	addr := startCtlServer(t, d, "test-token")
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/ctl/nat", bytes.NewBufferString(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Ctl-Token", "test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204 (body %s)", resp.StatusCode, body)
	}
	if got := f.actionCount("DeletePortMapping"); got != 0 {
		t.Fatalf("enable issued %d DeletePortMapping calls, want 0", got)
	}
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NATMapping == nil || !*cfg.NATMapping {
		t.Fatalf("persisted NATMapping = %v, want true", cfg.NATMapping)
	}
}

func TestMapNATPortWith(t *testing.T) {
	const port = 51820
	const lanIP = "192.168.1.5"
	ctx := context.Background()

	t.Run("prefers UPnP", func(t *testing.T) {
		defaultCalls := 0
		natpmpCalls := 0
		pcpCalls := 0
		upnp := func(context.Context, int, string) (string, natResult, error) {
			return "192.0.2.1", natResult{externalIP: "203.0.113.10", externalPort: 40000, via: "upnp"}, nil
		}
		natpmp := func(context.Context, int, string, string) (natResult, error) {
			natpmpCalls++
			return natResult{via: "natpmp"}, nil
		}
		pcp := func(context.Context, int, string, string) (natResult, error) {
			pcpCalls++
			return natResult{via: "pcp"}, nil
		}
		res, err := mapNATPortWith(ctx, port, lanIP, func() string {
			defaultCalls++
			return "192.0.2.1"
		}, upnp, natpmp, pcp)
		if err != nil || res.via != "upnp" {
			t.Fatalf("res=%+v err=%v, want via upnp", res, err)
		}
		if defaultCalls != 0 || natpmpCalls != 0 || pcpCalls != 0 {
			t.Fatalf("calls default=%d natpmp=%d pcp=%d, want 0/0/0", defaultCalls, natpmpCalls, pcpCalls)
		}
	})

	t.Run("falls back to NAT-PMP", func(t *testing.T) {
		defaultCalls := 0
		pcpCalls := 0
		upnp := func(context.Context, int, string) (string, natResult, error) {
			return "192.0.2.1", natResult{}, errors.New("upnp failed")
		}
		natpmp := func(context.Context, int, string, string) (natResult, error) {
			return natResult{externalIP: "203.0.113.20", externalPort: 40001, via: "natpmp"}, nil
		}
		pcp := func(context.Context, int, string, string) (natResult, error) {
			pcpCalls++
			return natResult{via: "pcp"}, nil
		}
		res, err := mapNATPortWith(ctx, port, lanIP, func() string {
			defaultCalls++
			return "192.0.2.1"
		}, upnp, natpmp, pcp)
		if err != nil || res.via != "natpmp" || res.externalPort != 40001 {
			t.Fatalf("res=%+v err=%v, want via natpmp port 40001", res, err)
		}
		if defaultCalls != 0 || pcpCalls != 0 {
			t.Fatalf("calls default=%d pcp=%d, want 0/0", defaultCalls, pcpCalls)
		}
	})

	t.Run("falls back to PCP", func(t *testing.T) {
		upnp := func(context.Context, int, string) (string, natResult, error) {
			return "192.0.2.1", natResult{}, errors.New("upnp failed")
		}
		natpmp := func(context.Context, int, string, string) (natResult, error) {
			return natResult{}, errors.New("natpmp failed")
		}
		pcp := func(context.Context, int, string, string) (natResult, error) {
			return natResult{externalIP: "203.0.113.30", externalPort: 40002, via: "pcp"}, nil
		}
		res, err := mapNATPortWith(ctx, port, lanIP, func() string { return "192.0.2.1" }, upnp, natpmp, pcp)
		if err != nil || res.via != "pcp" || res.externalPort != 40002 {
			t.Fatalf("res=%+v err=%v, want via pcp port 40002", res, err)
		}
	})

	t.Run("no gateway", func(t *testing.T) {
		defaultCalls := 0
		natpmpCalls := 0
		pcpCalls := 0
		upnp := func(context.Context, int, string) (string, natResult, error) {
			return "", natResult{}, errors.New("upnp failed")
		}
		natpmp := func(context.Context, int, string, string) (natResult, error) {
			natpmpCalls++
			return natResult{via: "natpmp"}, nil
		}
		pcp := func(context.Context, int, string, string) (natResult, error) {
			pcpCalls++
			return natResult{via: "pcp"}, nil
		}
		res, err := mapNATPortWith(ctx, port, lanIP, func() string {
			defaultCalls++
			return ""
		}, upnp, natpmp, pcp)
		if err == nil {
			t.Fatalf("res=%+v err=nil, want gateway error", res)
		}
		if defaultCalls != 1 || natpmpCalls != 0 || pcpCalls != 0 {
			t.Fatalf("calls default=%d natpmp=%d pcp=%d, want 1/0/0", defaultCalls, natpmpCalls, pcpCalls)
		}
	})
}

func TestNATClaimRequiresServer(t *testing.T) {
	n := newNatManager(&Config{}, context.Background())
	if err := n.claim(51820, ""); err == nil {
		t.Fatal("claim without server must return an error")
	}
	if n.claimedPort(51820) {
		t.Fatal("claim without server must not register the port")
	}
}

package diameter

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"go.uber.org/zap"
	xcontext "golang.org/x/net/context"

	"github.com/svinson1121/vectorcore-hss/internal/config"
)

// fakeConn is a minimal diam.Conn whose closure is controlled by the test.
type fakeConn struct {
	name   string
	closed chan struct{}
}

func newFakeConn(name string) *fakeConn {
	return &fakeConn{name: name, closed: make(chan struct{})}
}

func (c *fakeConn) Write(b []byte) (int, error)               { return len(b), nil }
func (c *fakeConn) WriteStream(b []byte, _ uint) (int, error) { return len(b), nil }
func (c *fakeConn) Close()                                    {}
func (c *fakeConn) LocalAddr() net.Addr                       { return fakeAddr("local") }
func (c *fakeConn) RemoteAddr() net.Addr                      { return fakeAddr(c.name) }
func (c *fakeConn) TLS() *tls.ConnectionState                 { return nil }
func (c *fakeConn) Dictionary() *dict.Parser                  { return dict.Default }
func (c *fakeConn) Context() xcontext.Context                 { return xcontext.Background() }
func (c *fakeConn) SetContext(xcontext.Context)               {}
func (c *fakeConn) Connection() net.Conn                      { return nil }
func (c *fakeConn) CloseNotify() <-chan struct{}              { return c.closed }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestConnTracker_DeleteConnRemovesAllEntriesForConn(t *testing.T) {
	ct := newConnTracker(nil, nil)
	dra := newFakeConn("dra")
	mme := newFakeConn("mme")
	// Two remote nodes reached through the same DRA connection.
	ct.Set("smsc.test.net", dra)
	ct.Set("pgw.test.net", dra)
	ct.Set("mme.test.net", mme)

	removed := ct.DeleteConn(dra)

	if len(removed) != 2 {
		t.Fatalf("removed = %v, want 2 hosts", removed)
	}
	if _, ok := ct.Get("smsc.test.net"); ok {
		t.Error("smsc.test.net still tracked after its connection closed")
	}
	if _, ok := ct.Get("pgw.test.net"); ok {
		t.Error("pgw.test.net still tracked after its connection closed")
	}
	if got, ok := ct.Get("mme.test.net"); !ok || got != mme {
		t.Error("mme.test.net entry on another connection was removed")
	}
}

func TestConnTracker_DeleteConnKeepsReplacementConn(t *testing.T) {
	ct := newConnTracker(nil, nil)
	oldConn := newFakeConn("old")
	newConn := newFakeConn("new")
	ct.Set("smsc.test.net", oldConn)
	// Peer reconnects before the old connection's close is processed.
	ct.Set("smsc.test.net", newConn)

	if removed := ct.DeleteConn(oldConn); len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
	if got, ok := ct.Get("smsc.test.net"); !ok || got != newConn {
		t.Fatal("replacement connection removed by old connection's close")
	}
}

func TestConnTracker_SetIgnoresClosedConn(t *testing.T) {
	ct := newConnTracker(nil, nil)
	conn := newFakeConn("closed")
	close(conn.closed)

	ct.Set("smsc.test.net", conn)

	if _, ok := ct.Get("smsc.test.net"); ok {
		t.Fatal("closed connection was tracked")
	}
}

// startTestServer runs the real Diameter state machine on a loopback listener.
func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := &config.Config{}
	cfg.HSS.OriginHost = "hss.test.net"
	cfg.HSS.OriginRealm = "test.net"
	s, err := NewServer(cfg, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := diam.MultistreamListen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	srv := &diam.Server{Network: "tcp", Handler: s.handler(), Dict: dict.Default}
	go srv.Serve(ln)
	return s, ln.Addr().String()
}

// dialTestPeer connects as originHost advertising S6a and completes CER/CEA.
func dialTestPeer(t *testing.T, addr, originHost string) diam.Conn {
	t.Helper()
	cli := &sm.Client{
		Dict: dict.Default,
		Handler: sm.New(&sm.Settings{
			OriginHost:  datatype.DiameterIdentity(originHost),
			OriginRealm: datatype.DiameterIdentity("test.net"),
			VendorID:    datatype.Unsigned32(vendor3GPP),
			ProductName: datatype.UTF8String("conntrack-test"),
		}),
		MaxRetransmits:     1,
		RetransmitInterval: time.Second,
		SupportedVendorID: []*diam.AVP{
			diam.NewAVP(avp.SupportedVendorID, avp.Mbit, 0, datatype.Unsigned32(vendor3GPP)),
		},
		VendorSpecificApplicationID: []*diam.AVP{
			diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{
				AVP: []*diam.AVP{
					diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appIDS6a)),
					diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendor3GPP)),
				},
			}),
		},
	}
	conn, err := cli.DialNetwork("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s as %s: %v", addr, originHost, err)
	}
	return conn
}

// dialRelayPeer connects as originHost and sends a CER advertising only the
// Relay Application-Id, as vectorcore-dra does. It is hand-built because
// sm.Client refuses to advertise an application its dictionary lacks.
func dialRelayPeer(t *testing.T, addr, originHost string) diam.Conn {
	t.Helper()
	ceac := make(chan *diam.Message, 1)
	mux := diam.NewServeMux()
	mux.HandleFunc("CEA", func(_ diam.Conn, m *diam.Message) { ceac <- m })
	conn, err := diam.Dial(addr, mux, dict.Default)
	if err != nil {
		t.Fatalf("dial %s as %s: %v", addr, originHost, err)
	}
	cer := diam.NewRequest(diam.CapabilitiesExchange, 0, dict.Default)
	cer.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	cer.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.net"))
	cer.NewAVP(avp.HostIPAddress, avp.Mbit, 0, datatype.Address(net.ParseIP("127.0.0.1")))
	cer.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(0))
	cer.NewAVP(avp.ProductName, 0, 0, datatype.UTF8String("relay-test"))
	cer.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appIDRelay))
	if _, err := cer.WriteTo(conn); err != nil {
		t.Fatalf("write CER: %v", err)
	}
	select {
	case cea := <-ceac:
		rc, err := cea.FindAVP(avp.ResultCode, 0)
		if err != nil || rc.Data.(datatype.Unsigned32) != diam.Success {
			t.Fatalf("CEA for relay peer not successful: %v", cea)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no CEA for relay peer")
	}
	return conn
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Peers are reachable right after CER/CEA, untracked on disconnect, and
// tracked on their new connection after a reconnect — without sending any
// application request.
func TestServer_ConnTrackerFollowsPeerLifecycle(t *testing.T) {
	s, addr := startTestServer(t)
	const host = "smsc.test.net"

	conn1 := dialTestPeer(t, addr, host)
	waitFor(t, "peer tracked after CER/CEA", func() bool {
		_, ok := s.ct.Get(host)
		return ok
	})
	first, _ := s.ct.Get(host)

	conn1.Close()
	waitFor(t, "peer untracked after disconnect", func() bool {
		_, ok := s.ct.Get(host)
		return !ok
	})

	conn2 := dialTestPeer(t, addr, host)
	defer conn2.Close()
	waitFor(t, "peer re-tracked after reconnect", func() bool {
		c, ok := s.ct.Get(host)
		return ok && c != first
	})
	if _, ok := s.ct.List()[host]; !ok {
		t.Fatalf("List() = %v, want %s", s.ct.List(), host)
	}
}

func TestConnTracker_TrackedHostPreferredOverRelay(t *testing.T) {
	ct := newConnTracker(nil, nil)
	dra, smsc := newFakeConn("dra"), newFakeConn("smsc")
	ct.SetRelay("dra.test.net", dra)
	ct.Set("smsc.test.net", smsc)

	if got, ok := ct.GetConn("smsc.test.net"); !ok || got != smsc {
		t.Fatal("tracked destination not used ahead of the relay")
	}
}

func TestConnTracker_FallsBackToDetectedRelay(t *testing.T) {
	ct := newConnTracker(nil, nil)
	draA, draB := newFakeConn("dra-a"), newFakeConn("dra-b")
	ct.SetRelay("dra-b.test.net", draB)
	ct.SetRelay("dra-a.test.net", draA)

	// Stable choice: lowest Origin-Host while it stays connected.
	for range 3 {
		if got, ok := ct.GetConn("smsc.test.net"); !ok || got != draA {
			t.Fatal("untracked destination not routed via dra-a")
		}
	}

	ct.DeleteConn(draA)
	if got, ok := ct.GetConn("smsc.test.net"); !ok || got != draB {
		t.Fatal("did not fail over to dra-b after dra-a disconnected")
	}
	ct.DeleteConn(draB)
	if _, ok := ct.GetConn("smsc.test.net"); ok {
		t.Fatal("lookup succeeded with no relay connected")
	}
}

func TestConnTracker_ConfiguredRoutePeerPreferred(t *testing.T) {
	ct := newConnTracker(nil, []string{"dra-missing.test.net", "dra-cfg.test.net"})
	detected, configured := newFakeConn("detected"), newFakeConn("configured")
	ct.SetRelay("dra-auto.test.net", detected)
	// A configured route peer need not advertise Relay; it is tracked from CER.
	ct.Set("dra-cfg.test.net", configured)

	if got, ok := ct.GetConn("pgw.test.net"); !ok || got != configured {
		t.Fatal("configured route peer not preferred over the detected relay")
	}

	ct.DeleteConn(configured)
	if got, ok := ct.GetConn("pgw.test.net"); !ok || got != detected {
		t.Fatal("did not fall back to the detected relay when no configured peer is connected")
	}
}

func TestConnTracker_NoRelayMisses(t *testing.T) {
	ct := newConnTracker(nil, nil)
	ct.Set("mme.test.net", newFakeConn("mme"))

	if _, ok := ct.GetConn("smsc.test.net"); ok {
		t.Fatal("untracked destination resolved without any relay")
	}
}

func TestConnTracker_SetRelayIgnoresClosedConn(t *testing.T) {
	ct := newConnTracker(nil, nil)
	conn := newFakeConn("closed")
	close(conn.closed)

	ct.SetRelay("dra.test.net", conn)

	if _, ok := ct.GetConn("smsc.test.net"); ok {
		t.Fatal("closed relay connection used for fallback")
	}
}

// A real DRA CER (Relay Application-Id only) makes destinations the HSS has
// never seen reachable through it, and the fallback ends when it disconnects.
func TestServer_RelayPeerIsFallbackRoute(t *testing.T) {
	s, addr := startTestServer(t)

	dra := dialRelayPeer(t, addr, "dra.test.net")
	waitFor(t, "relay peer tracked after CER/CEA", func() bool {
		_, ok := s.ct.Get("dra.test.net")
		return ok
	})
	draConn, _ := s.ct.Get("dra.test.net")

	got, ok := s.ct.GetConn("smsc.behind-dra.test.net")
	if !ok || got != draConn {
		t.Fatal("untracked destination not routed via the relay peer")
	}

	dra.Close()
	waitFor(t, "relay fallback removed after disconnect", func() bool {
		_, ok := s.ct.GetConn("smsc.behind-dra.test.net")
		return !ok
	})
}

// A non-relay peer is never used as a fallback route.
func TestServer_NonRelayPeerIsNotFallbackRoute(t *testing.T) {
	s, addr := startTestServer(t)

	mme := dialTestPeer(t, addr, "mme.test.net")
	defer mme.Close()
	waitFor(t, "peer tracked after CER/CEA", func() bool {
		_, ok := s.ct.Get("mme.test.net")
		return ok
	})

	if _, ok := s.ct.GetConn("smsc.test.net"); ok {
		t.Fatal("non-relay peer used as a fallback route")
	}
}

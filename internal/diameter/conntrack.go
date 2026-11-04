package diameter

import (
	"slices"
	"sync"

	"github.com/fiorix/go-diameter/v4/diam"
	"go.uber.org/zap"
)

// appIDRelay is the Diameter Relay Application-Id (RFC 6733 §2.4), advertised
// in CER/CEA by relay agents such as a DRA.
const appIDRelay = uint32(0xFFFFFFFF)

// ConnTracker maps peer OriginHost → active Diameter connection.
// Entries are added when a peer completes CER/CEA (its own Origin-Host) and
// by the handler wrapper in server.go on every incoming request (the request's
// Origin-Host, which behind a DRA maps the remote node to the DRA connection).
// All entries for a connection are removed when it closes, so HSS-initiated
// requests (CLR, IDR, ALR, RAR, ...) never target a dead connection.
//
// GetConn falls back to a relay peer (DRA) when a destination is not tracked:
// behind a DRA a node is only learned once it sends a request, so after an HSS
// restart the SMS-SC, PGW or MME would otherwise be unreachable until then.
// Relays are the configured routePeers (in order) and then any peer that
// advertised the Relay Application-Id in its CER. With neither present the
// fallback never applies and lookups behave as a plain map.
type ConnTracker struct {
	mu    sync.RWMutex
	conns map[string]diam.Conn
	// relays holds connected peers that advertised the Relay Application-Id,
	// keyed by their own Origin-Host.
	relays     map[string]diam.Conn
	routePeers []string
	log        *zap.Logger
}

func newConnTracker(log *zap.Logger, routePeers []string) *ConnTracker {
	if log == nil {
		log = zap.NewNop()
	}
	return &ConnTracker{
		conns:      make(map[string]diam.Conn),
		relays:     make(map[string]diam.Conn),
		routePeers: slices.Clone(routePeers),
		log:        log,
	}
}

// SetRelay records originHost as a connected relay peer on conn, in addition
// to tracking it like Set. Closed connections are ignored, as in Set.
func (ct *ConnTracker) SetRelay(originHost string, conn diam.Conn) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if connClosed(conn) {
		return
	}
	ct.conns[originHost] = conn
	ct.relays[originHost] = conn
}

// Set maps originHost to conn. A connection that has already closed is
// ignored: a request handled concurrently with the disconnect would otherwise
// re-add an entry that DeleteConn has just removed. The closed check and the
// write happen under the same lock DeleteConn takes, so every interleaving
// ends with no entry for the closed connection.
func (ct *ConnTracker) Set(originHost string, conn diam.Conn) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if connClosed(conn) {
		return
	}
	ct.conns[originHost] = conn
}

func (ct *ConnTracker) Get(originHost string) (diam.Conn, bool) {
	ct.mu.RLock()
	conn, ok := ct.conns[originHost]
	ct.mu.RUnlock()
	return conn, ok
}

func (ct *ConnTracker) Delete(originHost string) {
	ct.mu.Lock()
	delete(ct.conns, originHost)
	ct.mu.Unlock()
}

// DeleteConn removes every entry that points at conn and returns the removed
// Origin-Hosts. Entries are matched by connection, not by name, so a peer that
// has already reconnected keeps the entry for its new connection.
func (ct *ConnTracker) DeleteConn(conn diam.Conn) []string {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	var removed []string
	for host, c := range ct.conns {
		if c == conn {
			delete(ct.conns, host)
			removed = append(removed, host)
		}
	}
	for host, c := range ct.relays {
		if c == conn {
			delete(ct.relays, host)
		}
	}
	return removed
}

// GetConn satisfies s6a.PeerLookup and the other PeerLookup interfaces used
// for HSS-initiated requests. A tracked destination always wins; otherwise the
// request is handed to a relay peer, which routes it on Destination-Host.
func (ct *ConnTracker) GetConn(originHost string) (diam.Conn, bool) {
	conn, via, ok := ct.resolve(originHost)
	if ok && via != "" {
		ct.log.Debug("diameter: destination not tracked — routing via relay peer",
			zap.String("dest_host", originHost),
			zap.String("relay", via))
	}
	return conn, ok
}

// resolve returns the connection for originHost and, when it came from the
// relay fallback, the relay's Origin-Host.
func (ct *ConnTracker) resolve(originHost string) (conn diam.Conn, via string, ok bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	if conn, ok := ct.conns[originHost]; ok {
		return conn, "", true
	}
	// Configured route peers first, in the operator's order. They are tracked
	// under their own Origin-Host from CER whether or not they advertise Relay.
	for _, host := range ct.routePeers {
		if conn, ok := ct.conns[host]; ok {
			return conn, host, true
		}
	}
	// Then detected relays, in a stable order so one DRA is used consistently
	// while it stays connected.
	hosts := make([]string, 0, len(ct.relays))
	for host := range ct.relays {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	if len(hosts) > 0 {
		return ct.relays[hosts[0]], hosts[0], true
	}
	return nil, "", false
}

// List returns a snapshot of all currently tracked peers as a map of
// OriginHost → remote address string.
func (ct *ConnTracker) List() map[string]string {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	out := make(map[string]string, len(ct.conns))
	for host, conn := range ct.conns {
		out[host] = conn.RemoteAddr().String()
	}
	return out
}

// connClosed reports whether conn's close notification has already fired.
// Connections that cannot report closure are treated as open.
func connClosed(conn diam.Conn) bool {
	cn, ok := conn.(diam.CloseNotifier)
	if !ok {
		return false
	}
	select {
	case <-cn.CloseNotify():
		return true
	default:
		return false
	}
}

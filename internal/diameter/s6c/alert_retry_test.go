package s6c

import (
	"context"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

const (
	retryTestInterval    = 5 * time.Minute
	retryTestMaxInterval = time.Hour
)

func registeredSubscriber(imsi string, smsRegistered bool) *models.Subscriber {
	mme := "mme01.test.net"
	return &models.Subscriber{IMSI: imsi, ServingMME: &mme, MMERegisteredForSMS: &smsRegistered}
}

func retryMWD(imsi, scAddr, scHost string) models.MessageWaitingData {
	return models.MessageWaitingData{
		IMSI:           imsi,
		SCAddress:      scAddr,
		SCOriginHost:   scHost,
		SCOriginRealm:  "smsc.realm.net",
		MWDStatusFlags: MWDStatusMNRF,
	}
}

func newRetryTestHandlers(store *s6cStore, conns map[string]diam.Conn) *Handlers {
	h := newTestHandlers(store)
	h.peers = &peerLookupStub{conns: conns}
	return h
}

func runSweep(h *Handlers, now time.Time) {
	h.alertRetrySweep(context.Background(), now, retryTestInterval, retryTestMaxInterval)
}

func TestAlertRetrySweep_SendsDueRecordAndRecordsAttempt(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000201"
	store.addSubscriber(registeredSubscriber(imsi, true))
	store.mwds[imsi] = []models.MessageWaitingData{retryMWD(imsi, "12125550201", "smsc1.test.net")}
	conn := &captureConn{}
	h := newRetryTestHandlers(store, map[string]diam.Conn{"smsc1.test.net": conn})

	runSweep(h, time.Now().UTC())

	if conn.buf.Len() == 0 {
		t.Fatal("no ALR written for a due, never-attempted MNRF record")
	}
	msg := parseWrittenMessage(t, conn)
	if msg.Header.CommandCode != cmdALR {
		t.Fatalf("command code = %d, want ALR %d", msg.Header.CommandCode, cmdALR)
	}
	if len(store.storedMWD) != 1 {
		t.Fatalf("StoreMWD called %d times, want 1 (attempt metadata)", len(store.storedMWD))
	}
	got := store.storedMWD[0]
	if got.lastAlertTrigger == nil || *got.lastAlertTrigger != string(AlertTriggerRetry) {
		t.Fatalf("last_alert_trigger = %v, want %q", got.lastAlertTrigger, AlertTriggerRetry)
	}
	if got.alertAttemptCount != 1 {
		t.Fatalf("alert_attempt_count = %d, want 1", got.alertAttemptCount)
	}
	var pending int
	h.pendingALR.Range(func(_, _ any) bool { pending++; return true })
	if pending != 1 {
		t.Fatalf("pending ALR entries = %d, want 1", pending)
	}
}

func TestAlertRetrySweep_SkipsMemoryCapacityExceeded(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000202"
	store.addSubscriber(registeredSubscriber(imsi, true))
	rec := retryMWD(imsi, "12125550202", "smsc1.test.net")
	rec.MWDStatusFlags = MWDStatusMCEF
	store.mwds[imsi] = []models.MessageWaitingData{rec}
	conn := &captureConn{}
	h := newRetryTestHandlers(store, map[string]diam.Conn{"smsc1.test.net": conn})

	runSweep(h, time.Now().UTC())

	if conn.buf.Len() != 0 {
		t.Fatal("ALR sent for MCEF record; it must wait for a memory-available NOR")
	}
}

func TestAlertRetrySweep_SkipsSubscriberNotSMSRegistered(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000203"
	store.addSubscriber(registeredSubscriber(imsi, false))
	store.mwds[imsi] = []models.MessageWaitingData{retryMWD(imsi, "12125550203", "smsc1.test.net")}
	conn := &captureConn{}
	h := newRetryTestHandlers(store, map[string]diam.Conn{"smsc1.test.net": conn})

	runSweep(h, time.Now().UTC())

	if conn.buf.Len() != 0 {
		t.Fatal("ALR sent for a subscriber that is not SMS-registered")
	}
}

func TestAlertRetrySweep_SMSCNotConnectedLeavesMetadata(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000204"
	store.addSubscriber(registeredSubscriber(imsi, true))
	store.mwds[imsi] = []models.MessageWaitingData{retryMWD(imsi, "12125550204", "smsc-down.test.net")}
	h := newRetryTestHandlers(store, map[string]diam.Conn{})

	runSweep(h, time.Now().UTC())

	if len(store.storedMWD) != 0 {
		t.Fatalf("StoreMWD called %d times, want 0: no attempt was made", len(store.storedMWD))
	}
	if len(store.deletedMWD) != 0 {
		t.Fatalf("DeleteMWD called %d times, want 0", len(store.deletedMWD))
	}
}

func TestAlertRetrySweep_OnlyConnectedSMSCAlerted(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000205"
	store.addSubscriber(registeredSubscriber(imsi, true))
	store.mwds[imsi] = []models.MessageWaitingData{
		retryMWD(imsi, "12125550205", "smsc-up.test.net"),
		retryMWD(imsi, "12125550206", "smsc-down.test.net"),
	}
	up := &captureConn{}
	h := newRetryTestHandlers(store, map[string]diam.Conn{"smsc-up.test.net": up})

	runSweep(h, time.Now().UTC())

	if up.buf.Len() == 0 {
		t.Fatal("no ALR sent to the connected SMS-SC")
	}
	if len(store.storedMWD) != 1 || store.storedMWD[0].scAddr != "12125550205" {
		t.Fatalf("attempts recorded = %+v, want only 12125550205", store.storedMWD)
	}
}

func TestAlertRetrySweep_RespectsBackoff(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name     string
		attempts uint32
		ago      time.Duration
		wantSent bool
	}{
		// 3 attempts → backoff 4×interval = 20m.
		{"backoff not elapsed", 3, 15 * time.Minute, false},
		{"backoff elapsed", 3, 25 * time.Minute, true},
		// Many attempts → capped at maxInterval.
		{"capped backoff not elapsed", 50, 59 * time.Minute, false},
		{"capped backoff elapsed", 50, 61 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newS6cStore()
			imsi := "001010000000210"
			store.addSubscriber(registeredSubscriber(imsi, true))
			rec := retryMWD(imsi, "12125550210", "smsc1.test.net")
			last := now.Add(-tc.ago)
			rec.LastAlertAttemptAt = &last
			rec.AlertAttemptCount = tc.attempts
			store.mwds[imsi] = []models.MessageWaitingData{rec}
			conn := &captureConn{}
			h := newRetryTestHandlers(store, map[string]diam.Conn{"smsc1.test.net": conn})

			runSweep(h, now)

			if sent := conn.buf.Len() != 0; sent != tc.wantSent {
				t.Fatalf("ALR sent = %v, want %v", sent, tc.wantSent)
			}
		})
	}
}

func TestAlertRetryBackoff(t *testing.T) {
	for _, tc := range []struct {
		attempts uint32
		want     time.Duration
	}{
		{0, 5 * time.Minute},
		{1, 5 * time.Minute},
		{2, 10 * time.Minute},
		{3, 20 * time.Minute},
		{4, 40 * time.Minute},
		{5, time.Hour},
		{1 << 31, time.Hour},
	} {
		if got := alertRetryBackoff(tc.attempts, retryTestInterval, retryTestMaxInterval); got != tc.want {
			t.Errorf("alertRetryBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

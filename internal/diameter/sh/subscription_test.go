// Added by itskimffs (2026-11), see NOTICE: Sh subscriptions to repository data.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package sh

import (
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

type sentPNR struct{ identity, host, realm, data string }

// capturePNR replaces the PNR sender with one that hands each PNR to the test.
func capturePNR(h *Handlers) chan sentPNR {
	ch := make(chan sentPNR, 8)
	h.sendPNR = func(identity, host, realm, data string) { ch <- sentPNR{identity, host, realm, data} }
	return ch
}

func snr(originHost string, reqType int, expiry time.Time, sendData bool, sis ...string) *diam.Message {
	m := shRequest(cmdSNR, originHost, publicIdentity(testIMPU))
	for _, si := range sis {
		m.NewAVP(avpServiceIndication, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(si))
	}
	if sendData {
		m.NewAVP(avpSendDataIndication, avp.Vbit, Vendor3GPP, datatype.Enumerated(userDataRequested))
	}
	m.NewAVP(avpSubsReqType, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(reqType))
	m.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceRepositoryData))
	if !expiry.IsZero() {
		m.NewAVP(avpExpiryTime, avp.Vbit, Vendor3GPP, datatype.Time(expiry))
	}
	return m
}

func expiryTime(t *testing.T, ans *diam.Message) time.Time {
	t.Helper()
	a, err := ans.FindAVP(avpExpiryTime, Vendor3GPP)
	if err != nil {
		t.Fatalf("SNA without Expiry-Time")
	}
	return time.Time(a.Data.(datatype.Time))
}

func noPNR(t *testing.T, ch chan sentPNR) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("unexpected PNR %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

func onePNR(t *testing.T, ch chan sentPNR) sentPNR {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatalf("no PNR")
	}
	return sentPNR{}
}

func TestSubscribeAndNotify(t *testing.T) {
	h, _ := repoHandlers(t)
	pnrs := capturePNR(h)

	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	ans := call(t, h.SNR, snr(testAS, subsReqTypeSubscribe, expiry, false, mmtel))
	if code := resultCode(t, ans); code != diam.Success {
		t.Fatalf("SNR: %d", code)
	}
	if got := expiryTime(t, ans); !got.Equal(expiry) {
		t.Fatalf("Expiry-Time %v, want %v", got, expiry)
	}

	// A change by the subscribed AS itself is not sent back to it.
	if code := resultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs))); code != diam.Success {
		t.Fatalf("PUR: %d", code)
	}
	noPNR(t, pnrs)

	// A change from elsewhere, such as the API, is.
	h.NotifyRepositoryData(1, mmtel, "")
	p := onePNR(t, pnrs)
	if p.identity != testIMPU || p.host != testAS || p.realm != "ims.example.net" {
		t.Fatalf("PNR to %+v", p)
	}
	if !strings.Contains(p.data, "<SequenceNumber>0</SequenceNumber><ServiceData>"+simservs+"</ServiceData>") {
		t.Fatalf("PNR User-Data %s", p.data)
	}

	// Removed data is a RepositoryData without ServiceData.
	if code := resultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 1, ""))); code != diam.Success {
		t.Fatalf("PUR remove: %d", code)
	}
	h.NotifyRepositoryData(1, mmtel, "")
	if p := onePNR(t, pnrs); strings.Contains(p.data, "ServiceData") || !strings.Contains(p.data, "<ServiceIndication>MMTEL-Services</ServiceIndication><SequenceNumber>0</SequenceNumber>") {
		t.Fatalf("PNR User-Data for removed data %s", p.data)
	}

	// Another Service-Indication is not notified.
	h.NotifyRepositoryData(1, "other", "")
	noPNR(t, pnrs)

	// After unsubscribing, nothing is sent; a second unsubscribe has nothing to end.
	if code := resultCode(t, call(t, h.SNR, snr(testAS, subsReqTypeUnsubscribe, time.Time{}, false, mmtel))); code != diam.Success {
		t.Fatalf("unsubscribe: %d", code)
	}
	h.NotifyRepositoryData(1, mmtel, "")
	noPNR(t, pnrs)
	if code := resultCode(t, call(t, h.SNR, snr(testAS, subsReqTypeUnsubscribe, time.Time{}, false, mmtel))); code != DiameterErrorSubsDataAbsent {
		t.Fatalf("second unsubscribe: %d", code)
	}
}

func TestPURNotifiesOtherAS(t *testing.T) {
	h, _ := repoHandlers(t)
	h.repositoryAS["other.ims.example.net"] = true
	pnrs := capturePNR(h)
	if code := resultCode(t, call(t, h.SNR, snr(testAS, subsReqTypeSubscribe, time.Time{}, false, mmtel))); code != diam.Success {
		t.Fatalf("SNR: %d", code)
	}
	if code := resultCode(t, call(t, h.PUR, pur("other.ims.example.net", publicIdentity(testIMPU), mmtel, 0, simservs))); code != diam.Success {
		t.Fatalf("PUR: %d", code)
	}
	if p := onePNR(t, pnrs); p.host != testAS || !strings.Contains(p.data, simservs) {
		t.Fatalf("PNR %+v", p)
	}
}

func TestSubscriptionExpiry(t *testing.T) {
	h, _ := repoHandlers(t)
	pnrs := capturePNR(h)

	// Without Expiry-Time, or with a later one, the HSS grants maxSubscription.
	for _, asked := range []time.Time{{}, time.Now().Add(48 * time.Hour)} {
		ans := call(t, h.SNR, snr(testAS, subsReqTypeSubscribe, asked, false, mmtel))
		if got := time.Until(expiryTime(t, ans)); got > maxSubscription || got < maxSubscription-time.Minute {
			t.Fatalf("asked %v, granted %v", asked, got)
		}
	}
	// A renewal replaces the subscription, so one change is one PNR.
	h.NotifyRepositoryData(1, mmtel, "")
	onePNR(t, pnrs)
	noPNR(t, pnrs)

	// An expiry in the past is refused.
	if code := resultCode(t, call(t, h.SNR, snr(testAS, subsReqTypeSubscribe, time.Now().Add(-time.Minute), false, mmtel))); code != DiameterErrorUserDataNotRecognized {
		t.Fatalf("expired SNR: %d", code)
	}
	// An expired subscription is not notified.
	h.subscriptions.set(subscriptionKey{1, mmtel}, subscriber{host: testAS, identity: testIMPU, expiry: time.Now().Add(-time.Second)})
	h.NotifyRepositoryData(1, mmtel, "")
	noPNR(t, pnrs)
}

func TestSubscribeWithData(t *testing.T) {
	h, _ := repoHandlers(t)
	capturePNR(h)
	call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs))
	ans := call(t, h.SNR, snr(testAS, subsReqTypeSubscribe, time.Time{}, true, mmtel))
	if code := resultCode(t, ans); code != diam.Success {
		t.Fatalf("SNR: %d", code)
	}
	if got := userData(t, ans); !strings.Contains(got, simservs) {
		t.Fatalf("SNA User-Data %s", got)
	}
}

func TestSubscribeRefused(t *testing.T) {
	h, _ := repoHandlers(t)
	capturePNR(h)
	cases := []struct {
		name string
		req  *diam.Message
		want uint32
	}{
		{"AS not on the permission list", snr("other.example.net", subsReqTypeSubscribe, time.Time{}, false, mmtel), DiameterErrorUserDataCannotBeNotified},
		{"without Service-Indication", snr(testAS, subsReqTypeSubscribe, time.Time{}, false), DiameterErrorUserDataNotRecognized},
		{"unknown user", func() *diam.Message {
			m := shRequest(cmdSNR, testAS, publicIdentity("sip:4790000000@ims.example.net"))
			m.NewAVP(avpServiceIndication, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(mmtel))
			m.NewAVP(avpSubsReqType, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(subsReqTypeSubscribe))
			m.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceRepositoryData))
			return m
		}(), DiameterErrorUserUnknown},
		{"other Data-Reference", func() *diam.Message {
			m := shRequest(cmdSNR, testAS, publicIdentity(testIMPU))
			m.NewAVP(avpSubsReqType, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(subsReqTypeSubscribe))
			m.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceIMSUserState))
			return m
		}(), DiameterErrorUserDataCannotBeNotified},
	}
	for _, c := range cases {
		if code := resultCode(t, call(t, h.SNR, c.req)); code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
	}
}

func resultCode(t *testing.T, ans *diam.Message) uint32 {
	t.Helper()
	code, ok := shAnswerResultCode(ans)
	if !ok {
		t.Fatalf("answer without a result")
	}
	return code
}

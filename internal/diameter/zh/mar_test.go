// Added by itskimffs (2026-11), see NOTICE: Zh dictionary.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import (
	"bytes"
	"context"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/config"
	"github.com/svinson1121/vectorcore-hss/internal/diameter/basedict"
	"github.com/svinson1121/vectorcore-hss/internal/diameter/cx"
	"github.com/svinson1121/vectorcore-hss/internal/diameter/swx"
	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

const testIMSI = "001010000000001"

type marStore struct {
	repository.Repository
	auc *models.AUC
}

func (s *marStore) GetAUCByIMSI(_ context.Context, imsi string) (*models.AUC, error) {
	if s.auc != nil && s.auc.IMSI != nil && *s.auc.IMSI == imsi {
		return s.auc, nil
	}
	return nil, repository.ErrNotFound
}

func (s *marStore) AtomicGetAndIncrementSQN(_ context.Context, _ int, delta int64) (*models.AUC, error) {
	before := *s.auc
	s.auc.SQN += delta
	return &before, nil
}

func loadDicts(t *testing.T) {
	t.Helper()
	// The same order as the server.
	for name, load := range map[string]func() error{"base": basedict.Load} {
		if err := load(); err != nil {
			t.Fatalf("load %s dict: %v", name, err)
		}
	}
	_ = cx.LoadDict()
	_ = swx.LoadDict()
	if err := LoadDict(); err != nil {
		t.Fatalf("load zh dict: %v", err)
	}
}

// wire writes a message and reads it back, as the peer on the other side of the connection does.
func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	read, err := diam.ReadMessage(&buf, dict.Default)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return read
}

func zhMAR(impi string) *diam.Message {
	m := diam.NewRequest(303, AppIDZh, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bsf.example.net;1;1"))
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDZh)),
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(Vendor3GPP)),
	}})
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("bsf.example.net"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.net"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.net"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(impi))
	m.NewAVP(avpSIPNumberAuthItems, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Unsigned32(1))
	m.NewAVP(avpSIPAuthDataItem, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpSIPAuthenticationScheme, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.UTF8String(schemeDigestAKAv1)),
	}})
	return m
}

func handler() *Handlers {
	imsi := testIMSI
	store := &marStore{auc: &models.AUC{AUCID: 1, Ki: "465b5ce8b199b49faa5f0a2ee238a6bc", OPc: "cd63cb71954a9f4e48a5994e37a02baf",
		AMF: "8000", IMSI: &imsi}}
	return NewHandlers(&config.Config{HSS: config.HSSConfig{OriginHost: "hss.example.net", OriginRealm: "example.net"}}, store, zap.NewNop())
}

func child(g *diam.GroupedAVP, code uint32) *diam.AVP {
	for _, a := range g.AVP {
		if a.Code == code {
			return a
		}
	}
	return nil
}

// A BSF's MAR is read off the wire and answered with one Digest AKA vector, and the BSF can read the answer.
func TestMARFromTheWire(t *testing.T) {
	loadDicts(t)
	h := handler()
	ans, err := h.MAR(nil, wire(t, zhMAR(testIMSI+"@ims.mnc001.mcc001.3gppnetwork.org")))
	if err != nil {
		t.Fatalf("MAR: %v", err)
	}
	ans = wire(t, ans)
	rc, err := ans.FindAVP(avp.ResultCode, 0)
	if err != nil || rc.Data.(datatype.Unsigned32) != diam.Success {
		t.Fatalf("Result-Code: %v %v", rc, err)
	}
	if u, err := ans.FindAVP(avp.UserName, 0); err != nil || string(u.Data.(datatype.UTF8String)) != testIMSI {
		t.Errorf("User-Name: %v %v", u, err)
	}
	item, err := ans.FindAVP(avpSIPAuthDataItem, Vendor3GPP)
	if err != nil {
		t.Fatalf("SIP-Auth-Data-Item: %v", err)
	}
	g := item.Data.(*diam.GroupedAVP)
	if s := child(g, avpSIPAuthenticationScheme); s == nil || string(s.Data.(datatype.UTF8String)) != schemeDigestAKAv1 {
		t.Errorf("scheme: %v", s)
	}
	for code, want := range map[uint32]int{avpSIPAuthenticate: 32, avpSIPAuthorization: 8, avpConfidentialityKey: 16, avpIntegrityKey: 16} {
		a := child(g, code)
		if a == nil || len(a.Data.(datatype.OctetString)) != want {
			t.Errorf("AVP %d: %v, want %d bytes", code, a, want)
		}
	}
	if sqn := h.store.(*marStore).auc.SQN; sqn == 0 {
		t.Errorf("SQN was not advanced")
	}
}

func TestMARUnknownUser(t *testing.T) {
	loadDicts(t)
	ans, _ := handler().MAR(nil, wire(t, zhMAR("001019999999999@ims.example.net")))
	ans = wire(t, ans)
	if _, err := ans.FindAVP(avp.ExperimentalResult, 0); err != nil {
		if rc, err := ans.FindAVP(avp.ResultCode, 0); err != nil || uint32(rc.Data.(datatype.Unsigned32)) != DiameterErrorUserUnknown {
			t.Fatalf("want user unknown, got %v", ans)
		}
	}
}

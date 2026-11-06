// Added by itskimffs (2026-11), see NOTICE: Zh resynchronisation and GUSS.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import (
	"encoding/hex"
	"encoding/xml"
	"strings"
	"testing"

	milenage "github.com/emakeev/milenage"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// auts builds the RAND || AUTS a USIM sends for sqnMS (TS 33.102 6.3.3).
func auts(t *testing.T, h *Handlers, sqnMS uint64) []byte {
	t.Helper()
	auc := h.store.(*marStore).auc
	ki, _ := hex.DecodeString(auc.Ki)
	opc, _ := hex.DecodeString(auc.OPc)
	rand := []byte("0123456789abcdef")
	sqn := []byte{byte(sqnMS >> 40), byte(sqnMS >> 32), byte(sqnMS >> 24), byte(sqnMS >> 16), byte(sqnMS >> 8), byte(sqnMS)}
	akStar, err := milenage.F5Star(ki, rand, opc)
	if err != nil {
		t.Fatal(err)
	}
	_, macS, err := milenage.F1(ki, sqn, rand, opc, []byte{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte{}, rand...)
	for i := range sqn {
		out = append(out, sqn[i]^akStar[i])
	}
	return append(out, macS...)
}

// resyncMAR is a BSF's MAR after the UE answered with auts.
func resyncMAR(info []byte) *diam.Message {
	return zhMAR(testIMSI+"@ims.mnc001.mcc001.3gppnetwork.org",
		diam.NewAVP(avpSIPAuthorization, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(info)))
}

func resultCode(t *testing.T, ans *diam.Message) uint32 {
	t.Helper()
	if rc, err := ans.FindAVP(avp.ResultCode, 0); err == nil {
		return uint32(rc.Data.(datatype.Unsigned32))
	}
	er, err := ans.FindAVP(avp.ExperimentalResult, 0)
	if err != nil {
		t.Fatalf("no result in %v", ans)
	}
	for _, a := range er.Data.(*diam.GroupedAVP).AVP {
		if a.Code == avp.ExperimentalResultCode {
			return uint32(a.Data.(datatype.Unsigned32))
		}
	}
	t.Fatalf("no result code in %v", er)
	return 0
}

// The UE is ahead of the HSS: the HSS takes the UE's SQN and the new vector is above it.
func TestMARResync(t *testing.T) {
	loadDicts(t)
	h := handler()
	const sqnMS = 0x5000
	ans, err := h.MAR(nil, wire(t, resyncMAR(auts(t, h, sqnMS))))
	if err != nil {
		t.Fatalf("MAR: %v", err)
	}
	if rc := resultCode(t, wire(t, ans)); rc != diam.Success {
		t.Fatalf("result %d", rc)
	}
	// ResyncSQN sets SQN_MS + 100, the vector takes it and adds 32.
	if got := h.store.(*marStore).auc.SQN; got != sqnMS+100+32 {
		t.Errorf("SQN %#x, want %#x", got, sqnMS+100+32)
	}
}

// An AUTS that does not verify must not move the SQN.
func TestMARResyncBadMAC(t *testing.T) {
	loadDicts(t)
	h := handler()
	info := auts(t, h, 0x5000)
	info[len(info)-1] ^= 1
	ans, _ := h.MAR(nil, wire(t, resyncMAR(info)))
	if rc := resultCode(t, wire(t, ans)); rc == diam.Success {
		t.Fatal("bad AUTS accepted")
	}
	if got := h.store.(*marStore).auc.SQN; got != 0 {
		t.Errorf("SQN moved to %#x", got)
	}
	ans, _ = h.MAR(nil, wire(t, resyncMAR(info[:20])))
	if rc := resultCode(t, wire(t, ans)); rc == diam.Success {
		t.Fatal("short AUTS accepted")
	}
}

// The MAA carries the GUSS with the subscriber's IMPI and public identities.
func TestMARGUSS(t *testing.T) {
	loadDicts(t)
	h := handler()
	impi := testIMSI + "@ims.mnc001.mcc001.3gppnetwork.org"
	ans, err := h.MAR(nil, wire(t, zhMAR(impi)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := wire(t, ans).FindAVP(avpGBAUserSecSettings, Vendor3GPP)
	if err != nil {
		t.Fatalf("GBA-UserSecSettings: %v", err)
	}
	doc := string(a.Data.(datatype.OctetString))
	var g struct {
		ID   string `xml:"id,attr"`
		UICC string `xml:"bsfInfo>uiccType"`
		USS  []struct {
			ID   string   `xml:"id,attr"`
			Type string   `xml:"type,attr"`
			UIDs []string `xml:"uids>uid"`
		} `xml:"ussList>uss"`
	}
	if err := xml.Unmarshal([]byte(doc), &g); err != nil {
		t.Fatalf("%v: %s", err, doc)
	}
	if !strings.Contains(doc, `xmlns="urn:3gpp:gba:GBAGUSSSchema-R9:2010-02"`) || g.ID != impi || g.UICC != "GBA" ||
		len(g.USS) != 1 || g.USS[0].Type != "0" ||
		strings.Join(g.USS[0].UIDs, " ") != "tel:4790000001 sip:4790000001@ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("%s", doc)
	}
	if !strings.Contains(doc, "<flags></flags>") {
		t.Errorf("flags: %s", doc)
	}

	// No IMS subscriber: the bootstrap still works, without a GUSS.
	h.store.(*marStore).sub = nil
	ans, _ = h.MAR(nil, wire(t, zhMAR(impi)))
	ans = wire(t, ans)
	if rc := resultCode(t, ans); rc != diam.Success {
		t.Fatalf("result %d", rc)
	}
	if _, err := ans.FindAVP(avpGBAUserSecSettings, Vendor3GPP); err == nil {
		t.Error("GUSS without an IMS subscriber")
	}
}

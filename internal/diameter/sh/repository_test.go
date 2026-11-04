package sh

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/svinson1121/vectorcore-hss/internal/config"
	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository/postgres"
)

const (
	testAS   = "tas.ims.example.net"
	testIMPU = "sip:4794444444@ims.example.net"
	mmtel    = "MMTEL-Services"
	simservs = `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting active="true"/></simservs>`
)

// repoHandlers runs the handlers on the real store over SQLite, with one IMS
// subscriber, and testAS on the permission list.
func repoHandlers(t *testing.T) (*Handlers, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(models.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&models.IMSSubscriber{MSISDN: "4794444444", IMSI: ptr("001010000000001")}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := &config.Config{HSS: config.HSSConfig{OriginHost: "hss.example.net", OriginRealm: "example.net",
		ShRepositoryDataAS: []string{"TAS.ims.example.net."}}}
	return NewHandlers(cfg, postgres.New(db, 2), zap.NewNop(), &noopPeerLookup{}), db
}

// wire writes a message and reads it back with the dictionary, as the peer does.
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

func shRequest(code uint32, originHost string, userIdentity *diam.AVP) *diam.Message {
	m := diam.NewRequest(code, AppIDSh, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("tas.test;1;sh"))
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(Vendor3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDSh)),
	}})
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("ims.example.net"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.net"))
	m.AddAVP(userIdentity)
	return m
}

func publicIdentity(impu string) *diam.AVP {
	return diam.NewAVP(avpUserIdentity, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpPublicIdentity, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.UTF8String(impu)),
	}})
}

// pur builds a PUR for one RepositoryData. An empty data removes it.
func pur(originHost string, ui *diam.AVP, si string, seq int, data string) *diam.Message {
	m := shRequest(307, originHost, ui)
	m.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceRepositoryData))
	sd := ""
	if data != "" {
		sd = "<ServiceData>" + data + "</ServiceData>"
	}
	m.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><Sh-Data><RepositoryData><ServiceIndication>%s</ServiceIndication><SequenceNumber>%d</SequenceNumber>%s</RepositoryData></Sh-Data>`,
		si, seq, sd)))
	return m
}

func udrRepository(originHost string, sis ...string) *diam.Message {
	m := shRequest(306, originHost, publicIdentity(testIMPU))
	for _, si := range sis {
		m.NewAVP(avpServiceIndication, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(si))
	}
	m.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceRepositoryData))
	return m
}

func call(t *testing.T, fn func(diam.Conn, *diam.Message) (*diam.Message, error), req *diam.Message) *diam.Message {
	t.Helper()
	ans, err := fn(nil, wire(t, req))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return wire(t, ans)
}

func userData(t *testing.T, ans *diam.Message) string {
	t.Helper()
	a := findAVPDirect(ans, avpUserData, Vendor3GPP)
	if a == nil {
		return ""
	}
	return string(a.Data.(datatype.OctetString))
}

// The sequence of TS 29.328 §6.1.2: create with 0, update with n+1, remove with n+1.
func TestRepositoryDataLifecycle(t *testing.T) {
	h, _ := repoHandlers(t)

	// Nothing stored: success without User-Data.
	ans := call(t, h.UDR, udrRepository(testAS, mmtel))
	requireResultCode(t, ans, diam.Success)
	if ud := userData(t, ans); ud != "" {
		t.Fatalf("User-Data before any update: %s", ud)
	}

	ans = call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs))
	requireResultCode(t, ans, diam.Success)
	id := findAVPDirect(ans, avpRepositoryDataID, Vendor3GPP)
	if id == nil {
		t.Fatal("PUA without Repository-Data-ID")
	}

	ans = call(t, h.UDR, udrRepository(testAS, mmtel))
	requireResultCode(t, ans, diam.Success)
	want := "<RepositoryData><ServiceIndication>MMTEL-Services</ServiceIndication><SequenceNumber>0</SequenceNumber><ServiceData>" + simservs + "</ServiceData></RepositoryData>"
	if ud := userData(t, ans); !strings.Contains(ud, want) {
		t.Fatalf("User-Data = %s, want %s", ud, want)
	}

	changed := strings.Replace(simservs, "true", "false", 1)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 1, changed)), diam.Success)
	if ud := userData(t, call(t, h.UDR, udrRepository(testAS, mmtel))); !strings.Contains(ud, "<SequenceNumber>1</SequenceNumber><ServiceData>"+changed) {
		t.Fatalf("after update: %s", ud)
	}

	// The old number again is out of sync, and so is a jump.
	requireExperimentalResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 1, simservs)), DiameterErrorTransparentDataOutOfSync)
	requireExperimentalResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 3, simservs)), DiameterErrorTransparentDataOutOfSync)
	requireExperimentalResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), DiameterErrorTransparentDataOutOfSync)

	// Remove: n+1 without ServiceData.
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 2, "")), diam.Success)
	if ud := userData(t, call(t, h.UDR, udrRepository(testAS, mmtel))); ud != "" {
		t.Fatalf("after removal: %s", ud)
	}
	// Removing what is not there is not allowed, and creating starts at 0 again.
	requireExperimentalResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, "")), DiameterErrorOperationNotAllowed)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), diam.Success)
}

// After 65535 the next number is 1.
func TestRepositoryDataSequenceWraps(t *testing.T) {
	h, db := repoHandlers(t)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), diam.Success)
	if err := db.Model(&models.ShRepositoryData{}).Where("service_indication = ?", mmtel).Update("sequence_number", 65535).Error; err != nil {
		t.Fatal(err)
	}
	requireExperimentalResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), DiameterErrorTransparentDataOutOfSync)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 1, simservs)), diam.Success)
}

// Of several Service-Indications, the missing ones come back without ServiceData.
func TestRepositoryDataPartlyStored(t *testing.T) {
	h, _ := repoHandlers(t)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), diam.Success)
	ud := userData(t, call(t, h.UDR, udrRepository(testAS, mmtel, "OTHER")))
	if !strings.Contains(ud, "<ServiceIndication>OTHER</ServiceIndication><SequenceNumber>0</SequenceNumber></RepositoryData>") ||
		!strings.Contains(ud, "<ServiceData>"+simservs+"</ServiceData>") {
		t.Fatalf("User-Data = %s", ud)
	}
}

func TestRepositoryDataRefused(t *testing.T) {
	h, _ := repoHandlers(t)
	msisdn := diam.NewAVP(avpUserIdentity, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpMSISDN, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString([]byte{0x74, 0x49, 0x44, 0x44, 0x44})),
	}})
	tooBig := "<x>" + strings.Repeat("a", MaxServiceData) + "</x>"
	otherRef := pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)
	otherRef.AVP[len(otherRef.AVP)-2] = diam.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceInitialFilterCriteria))
	badXML := shRequest(307, testAS, publicIdentity(testIMPU))
	badXML.NewAVP(avpDataReference, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(DataReferenceRepositoryData))
	badXML.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString("<Sh-Data><RepositoryData>"))

	for _, c := range []struct {
		name string
		fn   func(diam.Conn, *diam.Message) (*diam.Message, error)
		req  *diam.Message
		want uint32
	}{
		{"PUR from an AS not on the list", h.PUR, pur("other.example.net", publicIdentity(testIMPU), mmtel, 0, simservs), DiameterErrorUserDataCannotBeModified},
		{"UDR from an AS not on the list", h.UDR, udrRepository("other.example.net", mmtel), DiameterErrorUserDataCannotBeRead},
		{"PUR for other data than repository data", h.PUR, otherRef, DiameterErrorUserDataCannotBeModified},
		{"PUR for an unknown user", h.PUR, pur(testAS, publicIdentity("sip:4790000000@ims.example.net"), mmtel, 0, simservs), DiameterErrorUserUnknown},
		{"PUR keyed by MSISDN", h.PUR, pur(testAS, msisdn, mmtel, 0, simservs), DiameterErrorOperationNotAllowed},
		{"PUR with too much data", h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, tooBig), DiameterErrorTooMuchData},
		{"PUR with broken XML", h.PUR, badXML, DiameterErrorUserDataNotRecognized},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireExperimentalResultCode(t, call(t, c.fn, c.req), c.want)
		})
	}
}

// Without a Service-Indication, UDR for RepositoryData answers as before.
func TestUDRRepositoryDataWithoutServiceIndicationUnchanged(t *testing.T) {
	h, _ := repoHandlers(t)
	ans := call(t, h.UDR, udrRepository("other.example.net"))
	requireResultCode(t, ans, diam.Success)
	if ud := userData(t, ans); !strings.Contains(ud, "<Sh-Data") || strings.Contains(ud, "RepositoryData") {
		t.Fatalf("User-Data = %s", ud)
	}
}

// Removing an IMS subscriber removes its repository data.
func TestDeleteIMSSubscriberRemovesRepositoryData(t *testing.T) {
	h, db := repoHandlers(t)
	requireResultCode(t, call(t, h.PUR, pur(testAS, publicIdentity(testIMPU), mmtel, 0, simservs)), diam.Success)
	if err := h.store.DeleteIMSSubscriberByMSISDN(context.Background(), "4794444444"); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.ShRepositoryData{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

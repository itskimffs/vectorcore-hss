package s6c

import (
	"bytes"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

// outcomeWith wraps cause (any datatype) in SM-Delivery-Outcome{MME-SM-
// Delivery-Outcome{SM-Delivery-Cause}}, all with the 3GPP vendor.
func outcomeWith(cause datatype.Type) *diam.AVP {
	return diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
		&diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
				&diam.GroupedAVP{AVP: []*diam.AVP{
					diam.NewAVP(avpSMDeliveryCause, avp.Mbit|avp.Vbit, Vendor3GPP, cause),
				}}),
		}})
}

// roundTrip serializes msg and decodes it again with the default dictionary,
// so AVPs that are not in the dictionary arrive as datatype.Unknown exactly as
// they would from the network.
func roundTrip(t *testing.T, msg *diam.Message) *diam.Message {
	t.Helper()
	b, err := msg.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	out, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func rdsmWithOutcome(outcome *diam.AVP) *diam.Message {
	msg := diam.NewRequest(cmdRDSM, AppIDS6c, dict.Default)
	if outcome != nil {
		msg.InsertAVP(outcome)
	}
	return msg
}

func requireOutcome(t *testing.T, got SMDeliveryOutcomeResult, wantStatus deliveryOutcomeStatus, wantCause int32) {
	t.Helper()
	if got.Status != wantStatus || got.Cause != wantCause {
		t.Fatalf("outcome = {status %v, cause %d, problem %q}, want {status %v, cause %d}",
			got.Status, got.Cause, got.Problem, wantStatus, wantCause)
	}
}

func TestParseDeliveryOutcome_CauseTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause datatype.Type
	}{
		{"Enumerated", datatype.Enumerated(SMDeliveryCauseSuccessfulTransfer)},
		{"Unsigned32", datatype.Unsigned32(SMDeliveryCauseSuccessfulTransfer)},
		{"Integer32", datatype.Integer32(SMDeliveryCauseSuccessfulTransfer)},
		{"raw 4 bytes", datatype.Unknown{0, 0, 0, byte(SMDeliveryCauseSuccessfulTransfer)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDeliveryOutcome(rdsmWithOutcome(outcomeWith(tc.cause)))
			requireOutcome(t, got, deliveryOutcomeParsed, SMDeliveryCauseSuccessfulTransfer)
		})
	}
}

func TestParseDeliveryOutcome_VendorOmittedOnWire(t *testing.T) {
	cause := func(flags uint8, vendor uint32) *diam.AVP {
		return diam.NewAVP(avpSMDeliveryCause, flags, vendor, datatype.Enumerated(SMDeliveryCauseSuccessfulTransfer))
	}
	for _, tc := range []struct {
		name    string
		outcome *diam.AVP
	}{
		{"cause without vendor", diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
			&diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
					&diam.GroupedAVP{AVP: []*diam.AVP{cause(avp.Mbit, 0)}}),
			}})},
		{"node outcome without vendor", diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
			&diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit, 0,
					&diam.GroupedAVP{AVP: []*diam.AVP{cause(avp.Mbit|avp.Vbit, Vendor3GPP)}}),
			}})},
		{"outer outcome without vendor", diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit, 0,
			&diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP,
					&diam.GroupedAVP{AVP: []*diam.AVP{cause(avp.Mbit|avp.Vbit, Vendor3GPP)}}),
			}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDeliveryOutcome(roundTrip(t, rdsmWithOutcome(tc.outcome)))
			requireOutcome(t, got, deliveryOutcomeParsed, SMDeliveryCauseSuccessfulTransfer)
		})
	}
}

func TestParseDeliveryOutcome_Unusable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		outcome    *diam.AVP
		wantStatus deliveryOutcomeStatus
		wantCause  int32
	}{
		{"absent", nil, deliveryOutcomeAbsent, -1},
		{"undefined cause value", outcomeWith(datatype.Enumerated(7)), deliveryOutcomeUnknownCause, 7},
		{"outcome not grouped",
			diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString("x")),
			deliveryOutcomeMalformed, -1},
		{"no node sub-AVP",
			diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avpSMDeliveryCause, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(SMDeliveryCauseSuccessfulTransfer)),
			}}),
			deliveryOutcomeMalformed, -1},
		{"node sub-AVP without cause",
			diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
					diam.NewAVP(avpAbsentUserDiagnosticSM, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Unsigned32(1)),
				}}),
			}}),
			deliveryOutcomeMalformed, -1},
		{"cause of wrong width", outcomeWith(datatype.Unknown{0, 2}), deliveryOutcomeMalformed, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDeliveryOutcome(rdsmWithOutcome(tc.outcome))
			requireOutcome(t, got, tc.wantStatus, tc.wantCause)
			if tc.wantStatus != deliveryOutcomeAbsent && got.Problem == "" {
				t.Fatal("Problem not set for an unusable outcome")
			}
		})
	}
}

// A later node sub-AVP with a readable cause is used when an earlier one is
// unusable.
func TestParseDeliveryOutcome_SkipsUnusableNode(t *testing.T) {
	outcome := diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpMMEDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{}),
		diam.NewAVP(avpIPSMGWDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avpSMDeliveryCause, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Enumerated(SMDeliveryCauseAbsentUser)),
		}}),
	}})
	requireOutcome(t, parseDeliveryOutcome(rdsmWithOutcome(outcome)), deliveryOutcomeParsed, SMDeliveryCauseAbsentUser)
}

// The HSS-3 scenario end to end: a successful-transfer report whose cause is
// not Enumerated must clear MWD, not store it.
func TestRSDS_SuccessfulTransferWithUnsigned32CauseClearsMWD(t *testing.T) {
	store := newS6cStore()
	imsi, msisdn, scAddr := "001010000000401", "33612000401", "33600000401"
	store.addSubscriber(&models.Subscriber{IMSI: imsi, MSISDN: ptr(msisdn)})
	store.mwds[imsi] = []models.MessageWaitingData{mwdRecord(imsi, scAddr, MWDStatusMNRF)}

	ans, err := newTestHandlers(store).RDSMR(nil,
		buildRDSMByMSISDN(t, msisdn, scAddr, outcomeWith(datatype.Unsigned32(SMDeliveryCauseSuccessfulTransfer))))
	if err != nil {
		t.Fatalf("RDSMR returned error: %v", err)
	}

	requireResultCode(t, ans, 2001)
	if len(store.deletedMWD) != 1 || store.deletedMWD[0].scAddr != scAddr {
		t.Fatalf("deletedMWD = %+v, want one delete for %s", store.deletedMWD, scAddr)
	}
	if len(store.storedMWD) != 0 {
		t.Fatalf("storedMWD = %+v, want none for a successful transfer", store.storedMWD)
	}
	requireNoAVP(t, ans, avpMWDStatus, Vendor3GPP)
}

// Unusable outcomes are fail-safe: MWD is stored as MNRF and the request
// succeeds, so a real delivery failure still gets an alert later.
func TestRSDS_UnusableOutcomeStoresMNRF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome *diam.AVP
	}{
		{"undefined cause value", outcomeWith(datatype.Enumerated(7))},
		{"malformed", diam.NewAVP(avpSMDeliveryOutcome, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString("x"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newS6cStore()
			imsi, msisdn := "001010000000402", "33612000402"
			store.addSubscriber(&models.Subscriber{IMSI: imsi, MSISDN: ptr(msisdn)})

			ans, err := newTestHandlers(store).RDSMR(nil, buildRDSMByMSISDN(t, msisdn, "33600000402", tc.outcome))
			if err != nil {
				t.Fatalf("RDSMR returned error: %v", err)
			}

			requireResultCode(t, ans, 2001)
			requireMWDStatus(t, ans, MWDStatusMNRF)
			if len(store.storedMWD) != 1 || store.storedMWD[0].statusFlags != MWDStatusMNRF {
				t.Fatalf("storedMWD = %+v, want one MNRF record", store.storedMWD)
			}
		})
	}
}

// TestDictionaryNodeOutcomeAndDiagnosticCodes pins the S6c dictionary names to
// the TS 29.338 AVP code table (5.3.3.1/1), and the parser's constants to the
// same codes, so the MSC/SGSN pairs cannot be swapped again.
func TestDictionaryNodeOutcomeAndDiagnosticCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
	}{
		{"MME-Absent-User-Diagnostic-SM", 3313},
		{"MSC-Absent-User-Diagnostic-SM", 3314},
		{"SGSN-Absent-User-Diagnostic-SM", 3315},
		{"SM-Delivery-Outcome", 3316},
		{"MME-SM-Delivery-Outcome", 3317},
		{"MSC-SM-Delivery-Outcome", 3318},
		{"SGSN-SM-Delivery-Outcome", 3319},
		{"IP-SM-GW-SM-Delivery-Outcome", 3320},
		{"SM-Delivery-Cause", 3321},
		{"Absent-User-Diagnostic-SM", 3322},
	} {
		a, err := dict.Default.FindAVPWithVendor(AppIDS6c, tc.name, Vendor3GPP)
		if err != nil {
			t.Errorf("%s: not in dictionary: %v", tc.name, err)
			continue
		}
		if a.Code != tc.code {
			t.Errorf("%s: dictionary code %d, want %d", tc.name, a.Code, tc.code)
		}
	}

	for _, tc := range []struct {
		name      string
		got, want uint32
	}{
		{"avpMMEDeliveryOutcome", avpMMEDeliveryOutcome, 3317},
		{"avpMSCDeliveryOutcome", avpMSCDeliveryOutcome, 3318},
		{"avpSGSNDeliveryOutcome", avpSGSNDeliveryOutcome, 3319},
		{"avpIPSMGWDeliveryOutcome", avpIPSMGWDeliveryOutcome, 3320},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

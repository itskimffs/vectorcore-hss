package s6c

import (
	"errors"
	"testing"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

func mwdRecord(imsi, scAddr string, flags uint32) models.MessageWaitingData {
	return models.MessageWaitingData{
		IMSI:           imsi,
		SCAddress:      scAddr,
		SCOriginHost:   "smsc.test.net",
		SCOriginRealm:  "smsc.realm.net",
		MWDStatusFlags: flags,
	}
}

// seedSRISubscriber adds a subscriber with the given SMS registration and MWD
// records to store and returns store.
func seedSRISubscriber(t *testing.T, store *s6cStore, imsi, msisdn string, smsRegistered bool, records ...models.MessageWaitingData) *s6cStore {
	t.Helper()
	sub := &models.Subscriber{IMSI: imsi, MSISDN: ptr(msisdn)}
	if smsRegistered {
		sub.ServingMME = ptr("mme1.epc.mnc001.mcc001.3gppnetwork.org")
		sub.MMERegisteredForSMS = ptr(true)
	}
	store.addSubscriber(sub)
	if len(records) > 0 {
		store.mwds[imsi] = records
	}
	return store
}

func TestSRISM_AttachedWithMCEFReportsMCEF(t *testing.T) {
	imsi, msisdn := "001010000000301", "33612000301"
	store := seedSRISubscriber(t, newS6cStore(), imsi, msisdn, true,
		mwdRecord(imsi, "33600000301", MWDStatusMCEF))

	ans, err := newTestHandlers(store).SRISR(nil, buildSRISMByMSISDN(t, msisdn))
	if err != nil {
		t.Fatalf("SRISR returned error: %v", err)
	}

	requireResultCode(t, ans, 2001)
	requireServingNodeMMEName(t, ans, "mme1.epc.mnc001.mcc001.3gppnetwork.org")
	requireMWDStatus(t, ans, MWDStatusMCEF)
}

func TestSRISM_AttachedWithPendingMNRFOmitsMWDStatus(t *testing.T) {
	imsi, msisdn := "001010000000302", "33612000302"
	// MNRF left on the record while the ALR is outstanding.
	store := seedSRISubscriber(t, newS6cStore(), imsi, msisdn, true,
		mwdRecord(imsi, "33600000302", MWDStatusMNRF))

	ans, err := newTestHandlers(store).SRISR(nil, buildSRISMByMSISDN(t, msisdn))
	if err != nil {
		t.Fatalf("SRISR returned error: %v", err)
	}

	requireServingNodeMMEName(t, ans, "mme1.epc.mnc001.mcc001.3gppnetwork.org")
	requireNoAVP(t, ans, avpMWDStatus, Vendor3GPP)
}

func TestSRISM_AttachedWithMNRFAndMCEFReportsMCEFOnly(t *testing.T) {
	imsi, msisdn := "001010000000303", "33612000303"
	store := seedSRISubscriber(t, newS6cStore(), imsi, msisdn, true,
		mwdRecord(imsi, "33600000303", MWDStatusMNRF),
		mwdRecord(imsi, "33600000304", MWDStatusMCEF))

	ans, err := newTestHandlers(store).SRISR(nil, buildSRISMByMSISDN(t, msisdn))
	if err != nil {
		t.Fatalf("SRISR returned error: %v", err)
	}

	requireMWDStatus(t, ans, MWDStatusMCEF)
}

func TestSRISM_AbsentWithMCEFReportsMNRFAndMCEF(t *testing.T) {
	imsi, msisdn := "001010000000305", "33612000305"
	store := seedSRISubscriber(t, newS6cStore(), imsi, msisdn, false,
		mwdRecord(imsi, "33600000305", MWDStatusMCEF))

	ans, err := newTestHandlers(store).SRISR(nil, buildSRISMByMSISDN(t, msisdn))
	if err != nil {
		t.Fatalf("SRISR returned error: %v", err)
	}

	requireNoAVP(t, ans, avpServingNode, Vendor3GPP)
	requireMWDStatus(t, ans, MWDStatusMNRF|MWDStatusMCEF)
}

func TestSRISM_MWDLookupFailureStillRoutes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		smsRegistered bool
	}{
		{"attached", true},
		{"absent", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imsi, msisdn := "001010000000306", "33612000306"
			store := seedSRISubscriber(t, newS6cStore(), imsi, msisdn, tc.smsRegistered)
			store.getMWDErr = errors.New("db down")

			ans, err := newTestHandlers(store).SRISR(nil, buildSRISMByMSISDN(t, msisdn))
			if err != nil {
				t.Fatalf("SRISR returned error: %v", err)
			}

			requireResultCode(t, ans, 2001)
			if tc.smsRegistered {
				requireServingNodeMMEName(t, ans, "mme1.epc.mnc001.mcc001.3gppnetwork.org")
				requireNoAVP(t, ans, avpMWDStatus, Vendor3GPP)
			} else {
				requireMWDStatus(t, ans, MWDStatusMNRF)
			}
		})
	}
}

func TestRSDS_AnswerIncludesOtherSCFlags(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000310"
	msisdn := "33612000310"
	store.addSubscriber(&models.Subscriber{IMSI: imsi, MSISDN: ptr(msisdn)})
	// Memory full reported earlier by another SMS-SC.
	store.mwds[imsi] = []models.MessageWaitingData{mwdRecord(imsi, "33600000399", MWDStatusMCEF)}

	ans, err := newTestHandlers(store).RDSMR(nil,
		buildRDSMByMSISDN(t, msisdn, "33600000310", mmeOutcomeAVP(SMDeliveryCauseAbsentUser)))
	if err != nil {
		t.Fatalf("RDSMR returned error: %v", err)
	}

	requireResultCode(t, ans, 2001)
	requireMWDStatus(t, ans, MWDStatusMNRF|MWDStatusMCEF)
	// The stored record for this SC carries only its own flag.
	if len(store.storedMWD) != 1 || store.storedMWD[0].statusFlags != MWDStatusMNRF {
		t.Fatalf("storedMWD = %+v, want one MNRF record", store.storedMWD)
	}
}

func TestRSDS_AnswerReplacesSameSCFlags(t *testing.T) {
	store := newS6cStore()
	imsi := "001010000000311"
	msisdn := "33612000311"
	scAddr := "33600000311"
	store.addSubscriber(&models.Subscriber{IMSI: imsi, MSISDN: ptr(msisdn)})
	// Same SMS-SC previously reported memory full; now the UE is absent.
	store.mwds[imsi] = []models.MessageWaitingData{mwdRecord(imsi, scAddr, MWDStatusMCEF)}

	ans, err := newTestHandlers(store).RDSMR(nil,
		buildRDSMByMSISDN(t, msisdn, scAddr, mmeOutcomeAVP(SMDeliveryCauseAbsentUser)))
	if err != nil {
		t.Fatalf("RDSMR returned error: %v", err)
	}

	requireMWDStatus(t, ans, MWDStatusMNRF)
}

func TestSRIMWDStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     []uint32
		reachable bool
		want      uint32
	}{
		{"reachable, no MWD", nil, true, 0},
		{"reachable, pending MNRF", []uint32{MWDStatusMNRF}, true, 0},
		{"reachable, MCEF", []uint32{MWDStatusMCEF}, true, MWDStatusMCEF},
		{"reachable, MNRF+MCEF across SCs", []uint32{MWDStatusMNRF, MWDStatusMCEF}, true, MWDStatusMCEF},
		{"absent, no MWD", nil, false, MWDStatusMNRF},
		{"absent, MCEF", []uint32{MWDStatusMCEF}, false, MWDStatusMNRF | MWDStatusMCEF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var recs []models.MessageWaitingData
			for i, f := range tc.flags {
				recs = append(recs, mwdRecord("001010000000399", string(rune('a'+i)), f))
			}
			if got := sriMWDStatus(recs, tc.reachable); got != tc.want {
				t.Fatalf("sriMWDStatus = %#x, want %#x", got, tc.want)
			}
		})
	}
}

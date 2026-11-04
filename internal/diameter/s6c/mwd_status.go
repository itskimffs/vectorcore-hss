package s6c

import "github.com/svinson1121/vectorcore-hss/internal/models"

// storedMWDStatus returns the MWD-Status flags held across all of a
// subscriber's MWD records. MNRF and MCEF describe the subscriber, not a
// particular SMS-SC, while records are kept per SC-Address, so the subscriber's
// status is the union of its records.
func storedMWDStatus(records []models.MessageWaitingData) uint32 {
	var flags uint32
	for _, r := range records {
		flags |= r.MWDStatusFlags
	}
	return flags
}

// sriMWDStatus returns the MWD-Status for an SRI-SM answer.
//
// A stored MNRF only means an alert is still pending (records keep it until
// the SMS-SC acknowledges the ALR), so it is cleared when the UE is
// SMS-reachable: reporting "not reachable" alongside a Serving-Node would be
// contradictory. MCEF is kept, since registration says nothing about memory;
// it tells the SMS-SC to wait for a memory-available alert. When the UE is not
// SMS-reachable MNRF is always set. Zero means MWD-Status is omitted.
func sriMWDStatus(records []models.MessageWaitingData, smsReachable bool) uint32 {
	flags := storedMWDStatus(records)
	if smsReachable {
		return flags &^ MWDStatusMNRF
	}
	return flags | MWDStatusMNRF
}

// rsdsMWDStatus returns the MWD-Status for an RSDS answer: the flags just
// stored for scAddr plus those of the subscriber's records for other SMS-SCs.
// existing is the record set read before the store, so its scAddr entry (if
// any) has been overwritten and is skipped.
func rsdsMWDStatus(existing []models.MessageWaitingData, scAddr string, storedFlags uint32) uint32 {
	flags := storedFlags
	for _, r := range existing {
		if r.SCAddress != scAddr {
			flags |= r.MWDStatusFlags
		}
	}
	return flags
}

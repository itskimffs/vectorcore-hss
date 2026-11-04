package s6c

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// deliveryOutcomeStatus classifies how an RSDS SM-Delivery-Outcome was read.
type deliveryOutcomeStatus int

const (
	// deliveryOutcomeAbsent: no SM-Delivery-Outcome AVP in the request.
	deliveryOutcomeAbsent deliveryOutcomeStatus = iota
	// deliveryOutcomeParsed: a defined SM-Delivery-Cause (0, 1 or 2) was read.
	deliveryOutcomeParsed
	// deliveryOutcomeUnknownCause: an SM-Delivery-Cause was read but its value
	// is not one TS 29.338 defines.
	deliveryOutcomeUnknownCause
	// deliveryOutcomeMalformed: the AVP is present but no SM-Delivery-Cause
	// could be read from it.
	deliveryOutcomeMalformed
)

func (s deliveryOutcomeStatus) String() string {
	switch s {
	case deliveryOutcomeAbsent:
		return "absent"
	case deliveryOutcomeParsed:
		return "parsed"
	case deliveryOutcomeUnknownCause:
		return "unknown_cause"
	case deliveryOutcomeMalformed:
		return "malformed"
	default:
		return fmt.Sprintf("deliveryOutcomeStatus(%d)", int(s))
	}
}

// nodeOutcomeCodes are the node-specific sub-AVPs of SM-Delivery-Outcome
// (TS 29.338 §5.3.3.15-18). All share the same {SM-Delivery-Cause,
// Absent-User-Diagnostic-SM} structure.
var nodeOutcomeCodes = []uint32{avpMMEDeliveryOutcome, avpMSCDeliveryOutcome, avpSGSNDeliveryOutcome, avpIPSMGWDeliveryOutcome}

// parseDeliveryOutcome extracts the SM-Delivery-Cause from the SM-Delivery-
// Outcome grouped AVP (code 3316) and reports how it was read in Status.
//
// Decoding is tolerant of senders that omit the vendor ID: such an AVP is not
// found in the dictionary and arrives as raw datatype.Unknown bytes, so grouped
// AVPs are re-decoded from those bytes and the cause is accepted as Enumerated,
// Unsigned32, Integer32 or 4 raw bytes. Without this a successful-transfer
// report sent that way would read as "no cause" and store MWD.
//
// Cause is -1 when Status is absent or malformed. The caller treats every
// status other than a parsed SuccessfulTransfer or MemoryCapacityExceeded as
// AbsentUser: storing MWD for a delivered message costs one unneeded ALR,
// whereas dropping a real failure would lose the alert entirely.
func parseDeliveryOutcome(msg *diam.Message) SMDeliveryOutcomeResult {
	outer := findTopLevelAVP(msg, avpSMDeliveryOutcome)
	if outer == nil {
		return SMDeliveryOutcomeResult{Cause: -1, Status: deliveryOutcomeAbsent}
	}
	outerChildren, ok := groupedChildren(outer)
	if !ok {
		return malformedOutcome("SM-Delivery-Outcome is not a decodable grouped AVP")
	}

	problem := "no MME/MSC/SGSN/IP-SM-GW delivery outcome sub-AVP"
	for _, node := range outerChildren {
		if !isNodeOutcomeCode(node.Code) {
			continue
		}
		nodeChildren, ok := groupedChildren(node)
		if !ok {
			problem = fmt.Sprintf("delivery outcome sub-AVP %d is not a decodable grouped AVP", node.Code)
			continue
		}
		result := SMDeliveryOutcomeResult{Cause: -1}
		haveCause := false
		for _, inner := range nodeChildren {
			switch inner.Code {
			case avpSMDeliveryCause:
				if v, ok := avpInt32(inner.Data); ok {
					result.Cause = v
					haveCause = true
				}
			case avpAbsentUserDiagnosticSM:
				if v, ok := avpUint32(inner.Data); ok {
					result.AbsentUserDiagnostic = v
				}
			}
		}
		if !haveCause {
			problem = fmt.Sprintf("delivery outcome sub-AVP %d has no readable SM-Delivery-Cause", node.Code)
			continue
		}
		switch result.Cause {
		case SMDeliveryCauseMemoryCapacityExceeded, SMDeliveryCauseAbsentUser, SMDeliveryCauseSuccessfulTransfer:
			result.Status = deliveryOutcomeParsed
		default:
			result.Status = deliveryOutcomeUnknownCause
			result.Problem = fmt.Sprintf("undefined SM-Delivery-Cause %d", result.Cause)
		}
		return result
	}
	return malformedOutcome(problem)
}

func malformedOutcome(problem string) SMDeliveryOutcomeResult {
	return SMDeliveryOutcomeResult{Cause: -1, Status: deliveryOutcomeMalformed, Problem: problem}
}

func isNodeOutcomeCode(code uint32) bool {
	for _, c := range nodeOutcomeCodes {
		if code == c {
			return true
		}
	}
	return false
}

// findTopLevelAVP returns the first top-level AVP with code, whatever its
// vendor ID, so an outcome sent without the 3GPP vendor is still found.
func findTopLevelAVP(msg *diam.Message, code uint32) *diam.AVP {
	for _, a := range msg.AVP {
		if a.Code == code {
			return a
		}
	}
	return nil
}

// groupedChildren returns a grouped AVP's children, re-decoding raw bytes
// when the AVP was not found in the dictionary (e.g. vendor ID omitted).
func groupedChildren(a *diam.AVP) ([]*diam.AVP, bool) {
	switch v := a.Data.(type) {
	case *diam.GroupedAVP:
		return v.AVP, true
	case datatype.Unknown:
		g, err := diam.DecodeGrouped(datatype.Grouped(v), AppIDS6c, dict.Default)
		if err != nil || len(g.AVP) == 0 {
			return nil, false
		}
		return g.AVP, true
	default:
		return nil, false
	}
}

func avpInt32(d datatype.Type) (int32, bool) {
	switch v := d.(type) {
	case datatype.Enumerated:
		return int32(v), true
	case datatype.Integer32:
		return int32(v), true
	case datatype.Unsigned32:
		if v > math.MaxInt32 {
			return 0, false
		}
		return int32(v), true
	case datatype.Unknown:
		if len(v) != 4 {
			return 0, false
		}
		return int32(binary.BigEndian.Uint32(v)), true
	default:
		return 0, false
	}
}

func avpUint32(d datatype.Type) (uint32, bool) {
	switch v := d.(type) {
	case datatype.Unsigned32:
		return uint32(v), true
	case datatype.Unknown:
		if len(v) != 4 {
			return 0, false
		}
		return binary.BigEndian.Uint32(v), true
	default:
		return 0, false
	}
}

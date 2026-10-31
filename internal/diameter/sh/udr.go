package sh

import (
	"context"
	"strings"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/diameter/avputil"
	"github.com/svinson1121/vectorcore-hss/internal/diameter/tbcd"
	"github.com/svinson1121/vectorcore-hss/internal/ims"
	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

// normalizePublicIdentity strips URI scheme prefixes from a Sh Public-Identity
// so the bare number or IMSI can be matched against DB columns. Handles:
//
//	tel:+13342012832           → 13342012832
//	tel:3342012832             → 3342012832
//	sip:+13342012832@ims.mnc… → 13342012832
//	3342012832                 → 3342012832 (pass-through)
func normalizePublicIdentity(identity string) string {
	s := identity
	switch {
	case strings.HasPrefix(s, "tel:"):
		s = strings.TrimPrefix(s, "tel:")
	case strings.HasPrefix(s, "sips:"):
		s = strings.TrimPrefix(s, "sips:")
		if idx := strings.IndexByte(s, '@'); idx >= 0 {
			s = s[:idx]
		}
	case strings.HasPrefix(s, "sip:"):
		s = strings.TrimPrefix(s, "sip:")
		if idx := strings.IndexByte(s, '@'); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimPrefix(s, "+")
}

// extractIdentity pulls the subscriber identity out of a User-Identity grouped
// AVP (code 700, vendor 10415). msg.Unmarshal does not reliably decode
// vendor-specific grouped AVPs via struct tags, so we use FindAVP + manual
// sub-AVP iteration instead. It returns an error for an MSISDN that is not
// valid TBCD (in the configured encoding).
func (h *Handlers) extractIdentity(msg *diam.Message) (string, error) {
	uiAVP, err := msg.FindAVP(avpUserIdentity, Vendor3GPP)
	if err != nil {
		return "", nil
	}
	grouped, ok := uiAVP.Data.(*diam.GroupedAVP)
	if !ok {
		return "", nil
	}
	for _, sub := range grouped.AVP {
		switch sub.Code {
		case avpPublicIdentity: // 601 — UTF8String
			if s, ok := sub.Data.(datatype.UTF8String); ok && len(s) > 0 {
				return string(s), nil
			}
		case avpMSISDN: // 701 — OctetString, TBCD-encoded
			if o, ok := sub.Data.(datatype.OctetString); ok && len(o) > 0 {
				return h.decodeMSISDN([]byte(o))
			}
		}
	}
	return "", nil
}

// lookupIMSSubscriber tries to find an IMS subscriber using several identity
// forms: exact, normalized (scheme/+ stripped), and with a "tel:" prefix
// (for DBs that store MSISDNs with that prefix).
func (h *Handlers) lookupIMSSubscriber(ctx context.Context, identity string) (*models.IMSSubscriber, error) {
	// 1. Exact match.
	sub, err := h.store.GetIMSSubscriberByMSISDN(ctx, identity)
	if err == nil {
		return sub, nil
	}
	if err != repository.ErrNotFound {
		return nil, err
	}

	sub, err = h.store.GetIMSSubscriberByIMSI(ctx, identity)
	if err == nil {
		return sub, nil
	}
	if err != repository.ErrNotFound {
		return nil, err
	}

	// 2. Normalized form (strip tel:, sip:@domain, leading +).
	normalized := normalizePublicIdentity(identity)
	if normalized != identity {
		sub, err = h.store.GetIMSSubscriberByMSISDN(ctx, normalized)
		if err == nil {
			return sub, nil
		}
		if err != repository.ErrNotFound {
			return nil, err
		}

		sub, err = h.store.GetIMSSubscriberByIMSI(ctx, normalized)
		if err == nil {
			return sub, nil
		}
		if err != repository.ErrNotFound {
			return nil, err
		}
	}

	// 3. Try with "tel:" prefix in case the DB stores MSISDNs that way.
	telForm := "tel:" + normalized
	sub, err = h.store.GetIMSSubscriberByMSISDN(ctx, telForm)
	if err == nil {
		return sub, nil
	}
	if err != repository.ErrNotFound {
		return nil, err
	}

	return nil, repository.ErrNotFound
}

func (h *Handlers) UDR(conn diam.Conn, msg *diam.Message) (*diam.Message, error) {
	var udr UDR
	if err := msg.Unmarshal(&udr); err != nil {
		h.log.Error("sh: UDR unmarshal failed", zap.Error(err))
		return avputil.ConstructFailureAnswer(msg, "", h.originHost, h.originRealm, diam.UnableToComply), err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// msg.Unmarshal does not populate vendor-specific grouped AVPs reliably.
	// Extract User-Identity manually via FindAVP.
	identity, err := h.extractIdentity(msg)
	if err != nil {
		h.log.Warn("sh: UDR invalid MSISDN in User-Identity", zap.Error(err))
		return avputil.ConstructFailureAnswer(msg, udr.SessionID, h.originHost, h.originRealm, DiameterErrorUserUnknown), nil
	}
	if identity == "" {
		h.log.Warn("sh: UDR missing identity")
		return avputil.ConstructFailureAnswer(msg, udr.SessionID, h.originHost, h.originRealm, DiameterErrorUserUnknown), nil
	}

	sub, err := h.lookupIMSSubscriber(ctx, identity)
	if err == repository.ErrNotFound {
		h.log.Warn("sh: UDR unknown identity",
			zap.String("identity", identity),
			zap.Int32("data_reference", int32(udr.DataReference)),
		)
		return avputil.ConstructFailureAnswer(msg, udr.SessionID, h.originHost, h.originRealm, DiameterErrorUserUnknown), nil
	}
	if err != nil {
		return avputil.ConstructFailureAnswer(msg, udr.SessionID, h.originHost, h.originRealm, diam.UnableToComply), err
	}

	// Dispatch on Data-Reference to determine what the AS wants.
	dataRef := int32(udr.DataReference)
	switch dataRef {
	case DataReferenceRepositoryData:
		// If the AS previously stored an opaque blob via UDU, return it directly.
		// Otherwise fall through and return the dynamically built IMS profile —
		// the HSS knows the SCSCF, IMPU, and IMPI from Cx/Rx and should serve them.
		if sub.ShProfile != nil && *sub.ShProfile != "" {
			h.log.Debug("sh: UDR RepositoryData (stored blob)", zap.String("identity", identity))
			ans := avputil.ConstructSuccessAnswer(msg, udr.SessionID, h.originHost, h.originRealm, AppIDSh)
			ans.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(*sub.ShProfile))
			return ans, nil
		}
		// No AS-stored blob — fall through to build profile from HSS data.

	case DataReferenceIMSPublicIdentity,
		DataReferenceIMSUserState,
		DataReferenceSCSCFName,
		DataReferenceInitialFilterCriteria,
		DataReferenceLocationInformation,
		DataReferenceUserState,
		DataReferenceMSISDN:
		// IMS profile data — fall through to build the Sh User-Data XML.

	default:
		h.log.Warn("sh: UDR unrecognised Data-Reference",
			zap.String("identity", identity),
			zap.Int32("data_reference", dataRef),
		)
		return avputil.ConstructFailureAnswer(msg, udr.SessionID, h.originHost, h.originRealm, DiameterErrorNotSupportedUserData), nil
	}

	// IFC is only returned when explicitly requested (Data-Reference=13).
	// For all other references (including RepositoryData=0) it must be omitted
	// per 3GPP TS 29.328 §6.1.2 — data reference types are mutually exclusive.
	var ifc *models.IFCProfile
	if dataRef == DataReferenceInitialFilterCriteria && sub.IFCProfileID != nil {
		ifc, _ = h.store.GetIFCProfileByID(ctx, *sub.IFCProfileID)
	}
	profile := ims.BuildShUserData(sub, ifc, h.mcc, h.mnc)

	h.log.Debug("sh: UDR success",
		zap.String("identity", identity),
		zap.Int32("data_reference", dataRef),
	)
	ans := avputil.ConstructSuccessAnswer(msg, udr.SessionID, h.originHost, h.originRealm, AppIDSh)
	ans.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(profile))
	return ans, nil
}

// decodeMSISDN decodes a User-Identity MSISDN. TS 29.329 §6.3.2 defines it as
// bare TBCD digits, decoded strictly so that invalid nibbles or misplaced
// filler are rejected rather than looked up.
//
// With sh.msisdn_encoding: legacy_prefixed it first tries the non-standard
// length | NOA | TBCD form sent by older vectorcore-smsc releases. That form is
// only accepted when the length octet matches exactly and the NOA octet has
// its extension bit set; anything else is decoded as bare TBCD. The match is
// only ambiguous for a full-length (8-octet) bare number starting "70" whose
// fourth digit is 8 or 9, which is why the mode is opt-in.
func (h *Handlers) decodeMSISDN(b []byte) (string, error) {
	if h.legacyPrefixedMSISDN && len(b) >= 3 && int(b[0]) == len(b)-1 && b[1]&0x80 != 0 {
		if digits, err := tbcd.DecodeMSISDN(b[2:]); err == nil {
			return digits, nil
		}
	}
	return tbcd.DecodeMSISDN(b)
}

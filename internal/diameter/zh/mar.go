// Modified by itskimffs (2026-11), see NOTICE: Zh resynchronisation and GUSS.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import (
	"context"
	"strings"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/crypto"
	"github.com/svinson1121/vectorcore-hss/internal/diameter/avputil"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

const DiameterErrorUserUnknown = uint32(5001)

func (h *Handlers) MAR(conn diam.Conn, msg *diam.Message) (*diam.Message, error) {
	var mar MAR
	if err := msg.Unmarshal(&mar); err != nil {
		h.log.Error("zh: MAR unmarshal failed", zap.Error(err))
		return avputil.ConstructFailureAnswer(msg, "", h.originHost, h.originRealm, diam.UnableToComply), err
	}

	// User-Name carries the IMPI, typically IMSI@realm — strip the realm.
	impi := string(mar.UserName)
	imsi := strings.SplitN(impi, "@", 2)[0]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	auc, err := h.store.GetAUCByIMSI(ctx, imsi)
	if err == repository.ErrNotFound {
		h.log.Warn("zh: MAR unknown IMSI", zap.String("imsi", imsi), zap.String("impi", impi))
		return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, DiameterErrorUserUnknown), err
	}
	if err != nil {
		return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), err
	}

	profile, err := crypto.LoadProfile(ctx, h.store, auc.AlgorithmProfileID)
	if err != nil {
		h.log.Error("zh: MAR profile load failed", zap.String("imsi", imsi), zap.Error(err))
		return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), err
	}

	// A resynchronisation: SIP-Authorization is RAND || AUTS. Take the UE's
	// SQN from AUTS and continue above it, as S6a does.
	if mar.SIPAuthDataItem != nil && len(mar.SIPAuthDataItem.SIPAuthorization) > 0 {
		resync := []byte(mar.SIPAuthDataItem.SIPAuthorization)
		if len(resync) != 30 {
			h.log.Warn("zh: MAR resync with bad length", zap.String("imsi", imsi), zap.Int("bytes", len(resync)))
			return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), nil
		}
		newSQN, err := crypto.ResyncSQNFull(auc, profile, resync)
		if err != nil {
			h.log.Warn("zh: MAR resync failed", zap.String("imsi", imsi), zap.Error(err))
			return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), nil
		}
		if err := h.store.ResyncSQN(ctx, auc.AUCID, newSQN+100); err != nil {
			h.log.Error("zh: MAR resync update failed", zap.String("imsi", imsi), zap.Error(err))
			return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), err
		}
		if auc, err = h.store.GetAUCByIMSI(ctx, imsi); err != nil {
			h.log.Error("zh: MAR resync reload failed", zap.String("imsi", imsi), zap.Error(err))
			return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), err
		}
		h.log.Info("zh: MAR resynchronised SQN", zap.String("imsi", imsi), zap.Int64("ue_sqn", newSQN))
	}

	vec, err := crypto.GenerateEAPAKAVector(auc, profile, h.store, ctx)
	if err != nil {
		h.log.Error("zh: MAR vector generation failed", zap.String("imsi", imsi), zap.Error(err))
		return avputil.ConstructFailureAnswer(msg, mar.SessionID, h.originHost, h.originRealm, avputil.DiameterAuthenticationDataUnavailable), err
	}

	h.log.Debug("zh: MAR success", zap.String("imsi", imsi))

	// SIP-Authenticate carries RAND || AUTN (32 bytes), same as SWx.
	sipAuthenticate := append(vec.RAND, vec.AUTN...)

	authDataAVPs := []*diam.AVP{
		diam.NewAVP(avpSIPItemNumber, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Unsigned32(1)),
		diam.NewAVP(avpSIPAuthenticationScheme, avp.Mbit|avp.Vbit, Vendor3GPP,
			datatype.UTF8String(schemeDigestAKAv1)),
		diam.NewAVP(avpSIPAuthenticate, avp.Mbit|avp.Vbit, Vendor3GPP,
			datatype.OctetString(sipAuthenticate)),
		diam.NewAVP(avpSIPAuthorization, avp.Mbit|avp.Vbit, Vendor3GPP,
			datatype.OctetString(vec.XRES)),
		diam.NewAVP(avpConfidentialityKey, avp.Mbit|avp.Vbit, Vendor3GPP,
			datatype.OctetString(vec.CK)),
		diam.NewAVP(avpIntegrityKey, avp.Mbit|avp.Vbit, Vendor3GPP,
			datatype.OctetString(vec.IK)),
	}

	ans := avputil.ConstructSuccessAnswer(msg, mar.SessionID, h.originHost, h.originRealm, AppIDZh)
	ans.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(imsi))
	ans.NewAVP(avpSIPNumberAuthItems, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.Unsigned32(1))
	ans.NewAVP(avpSIPAuthDataItem, avp.Mbit|avp.Vbit, Vendor3GPP,
		&diam.GroupedAVP{AVP: authDataAVPs})
	if g := h.guss(ctx, impi, imsi); g != nil {
		ans.NewAVP(avpGBAUserSecSettings, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(g))
	}
	return ans, nil
}

// guss returns the GUSS for the subscriber, or nil when the IMSI has no IMS
// subscriber. GBA-UserSecSettings is optional in the MAA (TS 29.109 6.1.2),
// so a missing GUSS does not fail the bootstrap.
func (h *Handlers) guss(ctx context.Context, impi, imsi string) []byte {
	sub, err := h.store.GetIMSSubscriberByIMSI(ctx, imsi)
	if err != nil {
		if err != repository.ErrNotFound {
			h.log.Warn("zh: MAR IMS subscriber lookup failed, no GUSS", zap.String("imsi", imsi), zap.Error(err))
		}
		return nil
	}
	g, err := buildGUSS(impi, sub, h.mcc, h.mnc)
	if err != nil {
		h.log.Error("zh: MAR GUSS failed", zap.String("imsi", imsi), zap.Error(err))
		return nil
	}
	return g
}

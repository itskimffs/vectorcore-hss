// Modified by itskimffs (2026-11), see NOTICE: Zh resynchronisation and GUSS.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import "github.com/fiorix/go-diameter/v4/diam/datatype"

const (
	Vendor3GPP = uint32(10415)

	// SIP AVP codes — same codes as Cx/SWx (already registered by SWx dict).
	avpSIPNumberAuthItems      = uint32(607)
	avpSIPAuthenticationScheme = uint32(608)
	avpSIPAuthenticate         = uint32(609)
	avpSIPAuthorization        = uint32(610)
	avpSIPAuthDataItem         = uint32(612)
	avpSIPItemNumber           = uint32(613)
	avpConfidentialityKey      = uint32(625)
	avpIntegrityKey            = uint32(626)

	// GBA-UserSecSettings (3GPP TS 29.109 6.3.1.1), the GUSS document.
	avpGBAUserSecSettings = uint32(400)

	// GBA authentication scheme (3GPP TS 33.220).
	schemeDigestAKAv1 = "Digest-AKAv1-MD5"
)

// MAR is the parsed Multimedia-Auth-Request from the BSF.
type MAR struct {
	SessionID   datatype.UTF8String       `avp:"Session-Id"`
	OriginHost  datatype.DiameterIdentity `avp:"Origin-Host"`
	OriginRealm datatype.DiameterIdentity `avp:"Origin-Realm"`
	// User-Name carries the IMPI, typically in NAI format: IMSI@realm.
	UserName datatype.UTF8String `avp:"User-Name"`
	// SIP-Auth-Data-Item carries RAND || AUTS in SIP-Authorization when the
	// UE asks for a resynchronisation (TS 29.109 6.1.1, TS 33.220 4.5.2).
	SIPAuthDataItem *MARSIPAuthDataItem `avp:"SIP-Auth-Data-Item,omitempty"`
}

type MARSIPAuthDataItem struct {
	SIPAuthenticationScheme datatype.UTF8String  `avp:"SIP-Authentication-Scheme,omitempty"`
	SIPAuthorization        datatype.OctetString `avp:"SIP-Authorization,omitempty"`
}

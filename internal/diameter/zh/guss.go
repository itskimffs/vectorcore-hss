// Added by itskimffs (2026-11), see NOTICE: Zh resynchronisation and GUSS.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import (
	"encoding/xml"
	"fmt"

	"github.com/svinson1121/vectorcore-hss/internal/ims"
	"github.com/svinson1121/vectorcore-hss/internal/models"
)

// gussNamespace is the GUSS schema of 3GPP TS 29.109 Annex A.
const gussNamespace = "urn:3gpp:gba:GBAGUSSSchema-R9:2010-02"

type guss struct {
	XMLName xml.Name `xml:"guss"`
	NS      string   `xml:"xmlns,attr"`
	ID      string   `xml:"id,attr"`
	BSFInfo struct {
		UICCType string `xml:"uiccType"`
	} `xml:"bsfInfo"`
	USS []uss `xml:"ussList>uss"`
}

type uss struct {
	ID   string   `xml:"id,attr"`
	Type int      `xml:"type,attr"`
	UIDs []string `xml:"uids>uid"`
	// flags is required by the schema and may be empty.
	Flags struct{} `xml:"flags"`
}

// buildGUSS returns the GBA user security settings for an IMS subscriber:
// GBA_ME, and one unspecific USS (type 0) with the subscriber's public
// identities, the same tel: and sip: URIs that Sh returns. Which NAF may use
// them is the BSF's decision, so the USS has no NAF group. The USS id is the
// GAA service identifier the BSF selects by, which for a 3GPP service type is
// the type code (TS 29.109 Annex A, NOTE 2).
func buildGUSS(impi string, sub *models.IMSSubscriber, mcc, mnc string) ([]byte, error) {
	domain := fmt.Sprintf("ims.mnc%s.mcc%s.3gppnetwork.org", ims.NormalizeMNC(mnc), mcc)
	g := guss{NS: gussNamespace, ID: impi}
	g.BSFInfo.UICCType = "GBA"
	g.USS = []uss{{ID: "0", Type: 0, UIDs: []string{
		"tel:" + sub.MSISDN,
		fmt.Sprintf("sip:%s@%s", sub.MSISDN, domain),
	}}}
	b, err := xml.Marshal(g)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

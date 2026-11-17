package ims

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

func escapeXML(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// BuildCxUserData generates the 3GPP Cx User-Data XML document (IMSSubscription,
// 3GPP TS 29.228 Annex D) for use in SAA Server-Assignment-Answer responses.
// The IFC profile xml_data (PublicIdentity blocks + InitialFilterCriteria) is
// embedded inside the ServiceProfile element.
func BuildCxUserData(sub *models.IMSSubscriber, ifc *models.IFCProfile, mcc, mnc string) string {
	imsi := ""
	if sub.IMSI != nil {
		imsi = *sub.IMSI
	}
	domain := imsDomain(mcc, mnc)
	privateID := fmt.Sprintf("%s@%s", imsi, domain)

	ifcContent := renderTemplate(sub, ifc, mcc, mnc)

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<IMSSubscription>
  <PrivateID>%s</PrivateID>
  <ServiceProfile>
    %s
  </ServiceProfile>
</IMSSubscription>`,
		escapeXML(privateID),
		ifcContent,
	)
}

// renderTemplate is the IFC profile's xml_data with {msisdn}, {mcc}, {mnc} and {imsi} filled in, or "" without a profile.
func renderTemplate(sub *models.IMSSubscriber, ifc *models.IFCProfile, mcc, mnc string) string {
	if ifc == nil {
		return ""
	}
	out := ifc.XMLData
	out = strings.ReplaceAll(out, "{msisdn}", sub.MSISDN)
	out = strings.ReplaceAll(out, "{mcc}", mcc)
	out = strings.ReplaceAll(out, "{mnc}", NormalizeMNC(mnc))
	if sub.IMSI != nil {
		out = strings.ReplaceAll(out, "{imsi}", *sub.IMSI)
	}
	return out
}

var identityRE = regexp.MustCompile(`(?s)<PublicIdentity>.*?<Identity>\s*([^<]*?)\s*</Identity>`)

// MSISDNIdentities are the public user identities that carry the subscriber's MSISDN, as the IFC profile (the one
// the S-CSCF gets in the SAA) names them: tel URIs first, then SIP URIs, each in the profile's order. An identity
// carries the MSISDN when its user part, without a leading +, is the MSISDN, so sip:+4794444444@ims and
// tel:4794444444 both count and the IMSI-derived identity does not. Without a profile, or when it names none, the
// identities are tel:<msisdn> and sip:<msisdn>@<domain> as before. Sh (PublicIdentifiers) and the GUSS (Zh) use
// this list, so an operator that moves the IMPUs to +E.164 in the profile gets the same identities everywhere.
func MSISDNIdentities(sub *models.IMSSubscriber, ifc *models.IFCProfile, mcc, mnc string) []string {
	var tels, sips []string
	for _, m := range identityRE.FindAllStringSubmatch(renderTemplate(sub, ifc, mcc, mnc), -1) {
		id := html.UnescapeString(m[1])
		scheme, rest, ok := strings.Cut(id, ":")
		if !ok {
			continue
		}
		user := rest
		if i := strings.IndexAny(user, "@;"); i >= 0 {
			user = user[:i]
		}
		if strings.TrimPrefix(user, "+") != sub.MSISDN || sub.MSISDN == "" {
			continue
		}
		switch strings.ToLower(scheme) {
		case "tel":
			tels = append(tels, id)
		case "sip", "sips":
			sips = append(sips, id)
		}
	}
	if len(tels)+len(sips) == 0 {
		return []string{"tel:" + sub.MSISDN, fmt.Sprintf("sip:%s@%s", sub.MSISDN, imsDomain(mcc, mnc))}
	}
	return append(tels, sips...)
}

// BuildShUserData generates the 3GPP Sh User-Data XML document (3GPP TS 29.328)
// for an IMS subscriber. If ifc is non-nil its XMLData is embedded inside the
// ServiceProfile element as the Initial Filter Criteria content. template is the
// subscriber's IFC profile, for the public identities only (it may be nil, and
// it is given even when the IFC content is not asked for).
func BuildShUserData(sub *models.IMSSubscriber, ifc, template *models.IFCProfile, mcc, mnc string) string {
	userState := 1 // UNREGISTERED
	if sub.SCSCF != nil && *sub.SCSCF != "" {
		userState = 0 // REGISTERED
	}

	imsi := ""
	if sub.IMSI != nil {
		imsi = *sub.IMSI
	}

	domain := imsDomain(mcc, mnc)
	privateIdentity := fmt.Sprintf("%s@%s", imsi, domain)

	// Public identities for the subscriber's MSISDN, from the IFC profile's template (MSISDNIdentities).
	var pubIDElems, pubIDBlocks strings.Builder
	for _, id := range MSISDNIdentities(sub, template, mcc, mnc) {
		fmt.Fprintf(&pubIDElems, "    <IMSPublicIdentity>%s</IMSPublicIdentity>\n", escapeXML(id))
		fmt.Fprintf(&pubIDBlocks, `      <PublicIdentity>
        <BarringIndication>0</BarringIndication>
        <Identity>%s</Identity>
      </PublicIdentity>
`, escapeXML(id))
	}

	ifcContent := renderTemplate(sub, ifc, mcc, mnc)

	scscfName := ""
	if sub.SCSCF != nil {
		scscfName = *sub.SCSCF
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Sh-Data xmlns="urn:3gpp:ns:pss:shDataType:7.0">
  <PublicIdentifiers>
%s    <MSISDN>%s</MSISDN>
  </PublicIdentifiers>
  <IMSUserState>%d</IMSUserState>
  <ShIMSData>
    <IMSPrivateUserIdentity>%s</IMSPrivateUserIdentity>
    <SCSCFName>%s</SCSCFName>
    <ServiceProfile>
%s      %s
    </ServiceProfile>
  </ShIMSData>
</Sh-Data>`,
		pubIDElems.String(),
		escapeXML(sub.MSISDN),
		userState,
		escapeXML(privateIdentity),
		escapeXML(scscfName),
		pubIDBlocks.String(),
		ifcContent,
	)
}

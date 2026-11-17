package ims

import (
	"strings"
	"testing"

	"github.com/svinson1121/vectorcore-hss/internal/models"
)

func TestNormalizeMNC(t *testing.T) {
	if got := NormalizeMNC("99"); got != "099" {
		t.Fatalf("NormalizeMNC(99) = %q, want %q", got, "099")
	}
	if got := NormalizeMNC("435"); got != "435" {
		t.Fatalf("NormalizeMNC(435) = %q, want %q", got, "435")
	}
}

func TestBuildCxUserDataPadsTwoDigitMNCInDomainAndIFCTemplate(t *testing.T) {
	imsi := "999990000000001"
	sub := &models.IMSSubscriber{
		IMSI:   &imsi,
		MSISDN: "15551234567",
	}
	ifc := &models.IFCProfile{
		XMLData: `<PublicIdentity><Identity>sip:{msisdn}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity></PublicIdentity>`,
	}

	got := BuildCxUserData(sub, ifc, "999", "99")

	if !strings.Contains(got, "<PrivateID>999990000000001@ims.mnc099.mcc999.3gppnetwork.org</PrivateID>") {
		t.Fatalf("private ID did not use padded MNC: %s", got)
	}
	if !strings.Contains(got, "sip:15551234567@ims.mnc099.mcc999.3gppnetwork.org") {
		t.Fatalf("IFC template did not use padded MNC: %s", got)
	}
}

func TestBuildShUserDataPadsTwoDigitMNCInDomainAndIFCTemplate(t *testing.T) {
	imsi := "999990000000001"
	sub := &models.IMSSubscriber{
		IMSI:   &imsi,
		MSISDN: "15551234567",
	}
	ifc := &models.IFCProfile{
		XMLData: `<InitialFilterCriteria><ApplicationServer><ServerName>sip:tas.ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</ServerName></ApplicationServer></InitialFilterCriteria>`,
	}

	got := BuildShUserData(sub, ifc, ifc, "999", "99")

	if !strings.Contains(got, "<IMSPrivateUserIdentity>999990000000001@ims.mnc099.mcc999.3gppnetwork.org</IMSPrivateUserIdentity>") {
		t.Fatalf("private identity did not use padded MNC: %s", got)
	}
	if !strings.Contains(got, "sip:15551234567@ims.mnc099.mcc999.3gppnetwork.org") {
		t.Fatalf("public identity did not use padded MNC: %s", got)
	}
	if !strings.Contains(got, "sip:tas.ims.mnc099.mcc999.3gppnetwork.org") {
		t.Fatalf("IFC template did not use padded MNC: %s", got)
	}
}

// defaultTemplate is KimTel-Core k8s/provision/ifc/default.xml's identities: the default IMPU, the tel alias and the
// IMSI-derived identity.
const defaultTemplate = `<PublicIdentity>
    <Identity>sip:{msisdn}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>tel:{msisdn}</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>sip:{imsi}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity>
</PublicIdentity>`

// e164Template has the +E.164 identities first and the old ones after them.
const e164Template = `<PublicIdentity>
    <Identity>sip:+{msisdn}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>tel:+{msisdn}</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>sip:{msisdn}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>tel:{msisdn}</Identity>
</PublicIdentity>
<PublicIdentity>
    <Identity>sip:{imsi}@ims.mnc{mnc}.mcc{mcc}.3gppnetwork.org</Identity>
</PublicIdentity>`

func TestMSISDNIdentities(t *testing.T) {
	imsi := "001010000182738"
	sub := &models.IMSSubscriber{IMSI: &imsi, MSISDN: "4794444444"}
	domain := "ims.mnc001.mcc001.3gppnetwork.org"
	for name, c := range map[string]struct {
		ifc  *models.IFCProfile
		want []string
	}{
		// the same list as before the template was used, so nothing changes for today's profile
		"no profile": {nil, []string{"tel:4794444444", "sip:4794444444@" + domain}},
		"default":    {&models.IFCProfile{XMLData: defaultTemplate}, []string{"tel:4794444444", "sip:4794444444@" + domain}},
		"e164": {&models.IFCProfile{XMLData: e164Template}, []string{
			"tel:+4794444444", "tel:4794444444", "sip:+4794444444@" + domain, "sip:4794444444@" + domain}},
		"no msisdn identity": {&models.IFCProfile{XMLData: `<PublicIdentity><Identity>sip:{imsi}@x</Identity></PublicIdentity>`},
			[]string{"tel:4794444444", "sip:4794444444@" + domain}},
	} {
		got := MSISDNIdentities(sub, c.ifc, "001", "01")
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestBuildShUserDataIdentitiesWithoutIFC(t *testing.T) {
	imsi := "001010000182738"
	sub := &models.IMSSubscriber{IMSI: &imsi, MSISDN: "4794444444"}
	got := BuildShUserData(sub, nil, &models.IFCProfile{XMLData: e164Template}, "001", "01")
	if !strings.Contains(got, "<IMSPublicIdentity>sip:+4794444444@ims.mnc001.mcc001.3gppnetwork.org</IMSPublicIdentity>") {
		t.Fatalf("no +E.164 identity: %s", got)
	}
	if strings.Contains(got, "InitialFilterCriteria") || strings.Contains(got, "sip:001010000182738@") {
		t.Fatalf("IFC content or the IMSI identity without Data-Reference 13: %s", got)
	}
}

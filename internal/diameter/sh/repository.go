package sh

// Repository data (transparent data) over Sh, 3GPP TS 29.328 §6.1.1 (Sh-Pull)
// and §6.1.2 (Sh-Update), Data-Reference RepositoryData (0). An Application
// Server stores a document per IMS Public User Identity and Service-Indication,
// such as the MMTel supplementary services under "MMTEL-Services" (TS 29.364).
//
// Not supported: Sh-Subs-Notif and Sh-Notif for repository data, the
// Update-Eff and Update-Eff-Enhance features (several instances in one request)
// and shared data for alias identities.

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/diameter/avputil"
	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

// MaxServiceData is the most service data the HSS accepts for one
// Service-Indication. More gives DIAMETER_ERROR_TOO_MUCH_DATA.
const MaxServiceData = 64 << 10

// RepositoryDataStore is implemented by the PostgreSQL store.
type RepositoryDataStore interface {
	GetShRepositoryData(ctx context.Context, imsSubscriberID int, serviceIndications []string) ([]models.ShRepositoryData, error)
	UpdateShRepositoryData(ctx context.Context, imsSubscriberID int, serviceIndication string,
		decide func(current *models.ShRepositoryData) (*models.ShRepositoryData, error)) error
}

// resultError carries the Experimental-Result-Code of a refused request.
type resultError uint32

func (e resultError) Error() string { return fmt.Sprintf("experimental result %d", uint32(e)) }

// shData is the part of the Sh-Data document (TS 29.328 Annex D) used here.
// Elements are matched by local name, with or without a namespace.
type shData struct {
	XMLName        xml.Name             `xml:"Sh-Data"`
	RepositoryData []repositoryDataElem `xml:"RepositoryData"`
}

type repositoryDataElem struct {
	ServiceIndication string           `xml:"ServiceIndication"`
	SequenceNumber    *int             `xml:"SequenceNumber"`
	ServiceData       *serviceDataElem `xml:"ServiceData"`
}

type serviceDataElem struct {
	Inner string `xml:",innerxml"`
}

// mayUseRepositoryData is the AS permission list of TS 29.328 §6.2 for
// repository data, by Origin-Host.
func (h *Handlers) mayUseRepositoryData(msg *diam.Message) bool {
	oh, err := msg.FindAVP(avp.OriginHost, 0)
	if err != nil {
		return false
	}
	return h.repositoryAS[strings.ToLower(strings.TrimSuffix(string(oh.Data.(datatype.DiameterIdentity)), "."))]
}

// hasPublicIdentity reports whether User-Identity holds a Public-Identity,
// which is the only access key for repository data (TS 29.328 table 7.6.1).
func hasPublicIdentity(msg *diam.Message) bool {
	ui, err := msg.FindAVP(avpUserIdentity, Vendor3GPP)
	if err != nil {
		return false
	}
	g, ok := ui.Data.(*diam.GroupedAVP)
	if !ok {
		return false
	}
	for _, a := range g.AVP {
		if a.Code == avpPublicIdentity {
			return true
		}
	}
	return false
}

func serviceIndications(msg *diam.Message) []string {
	avps, _ := msg.FindAVPs(avpServiceIndication, Vendor3GPP)
	var out []string
	for _, a := range avps {
		if o, ok := a.Data.(datatype.OctetString); ok && len(o) > 0 {
			out = append(out, string(o))
		}
	}
	return out
}

func sessionID(msg *diam.Message) datatype.UTF8String {
	if a, err := msg.FindAVP(avp.SessionID, 0); err == nil {
		if s, ok := a.Data.(datatype.UTF8String); ok {
			return s
		}
	}
	return ""
}

func (h *Handlers) fail(msg *diam.Message, code uint32) *diam.Message {
	ans := avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, code)
	addVendorSpecificApplicationID(ans)
	return ans
}

func addVendorSpecificApplicationID(ans *diam.Message) {
	ans.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(Vendor3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDSh)),
	}})
}

// pullRepositoryData answers a UDR for RepositoryData with Service-Indications.
// The subscriber has been found already.
func (h *Handlers) pullRepositoryData(ctx context.Context, msg *diam.Message, sub *models.IMSSubscriber, indications []string) (*diam.Message, error) {
	if h.repository == nil || !h.mayUseRepositoryData(msg) {
		return h.fail(msg, DiameterErrorUserDataCannotBeRead), nil
	}
	if !hasPublicIdentity(msg) {
		return h.fail(msg, DiameterErrorOperationNotAllowed), nil
	}
	rows, err := h.repository.GetShRepositoryData(ctx, sub.IMSSubscriberID, indications)
	if err != nil {
		return avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, diam.UnableToComply), err
	}
	ans := avputil.ConstructSuccessAnswer(msg, sessionID(msg), h.originHost, h.originRealm, AppIDSh)
	addVendorSpecificApplicationID(ans)
	// None of the requested data: no User-Data. Some: the missing ones are a
	// RepositoryData with a Service Indication and a Sequence Number but no
	// ServiceData (TS 29.328 §6.1.1).
	if len(rows) == 0 {
		return ans, nil
	}
	ans.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(repositoryDataXML(indications, rows)))
	return ans, nil
}

func repositoryDataXML(indications []string, rows []models.ShRepositoryData) string {
	stored := make(map[string]models.ShRepositoryData, len(rows))
	for _, r := range rows {
		stored[r.ServiceIndication] = r
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Sh-Data>`)
	seen := map[string]bool{}
	for _, si := range indications {
		if seen[si] {
			continue
		}
		seen[si] = true
		b.WriteString("<RepositoryData><ServiceIndication>")
		_ = xml.EscapeText(&b, []byte(si))
		b.WriteString("</ServiceIndication>")
		if r, ok := stored[si]; ok {
			fmt.Fprintf(&b, "<SequenceNumber>%d</SequenceNumber><ServiceData>%s</ServiceData>", r.SequenceNumber, r.ServiceData)
		} else {
			b.WriteString("<SequenceNumber>0</SequenceNumber>")
		}
		b.WriteString("</RepositoryData>")
	}
	b.WriteString("</Sh-Data>")
	return b.String()
}

// PUR handles Sh Profile-Update-Request (command 307), for repository data only.
func (h *Handlers) PUR(_ diam.Conn, msg *diam.Message) (*diam.Message, error) {
	// 1. The AS permission list. Only repository data can be updated here.
	refs, _ := msg.FindAVPs(avpDataReference, Vendor3GPP)
	if len(refs) != 1 {
		// More than one needs Update-Eff-Enhance, which this HSS does not support.
		return h.fail(msg, DiameterErrorOperationNotAllowed), nil
	}
	if ref, ok := refs[0].Data.(datatype.Enumerated); !ok || int32(ref) != DataReferenceRepositoryData {
		return h.fail(msg, DiameterErrorUserDataCannotBeModified), nil
	}
	if h.repository == nil || !h.mayUseRepositoryData(msg) {
		return h.fail(msg, DiameterErrorUserDataCannotBeModified), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 2. The user must exist. 3. Repository data is keyed by a public identity.
	identity := extractIdentity(msg)
	if identity == "" {
		return h.fail(msg, DiameterErrorUserUnknown), nil
	}
	sub, err := h.lookupIMSSubscriber(ctx, identity)
	if errors.Is(err, repository.ErrNotFound) {
		return h.fail(msg, DiameterErrorUserUnknown), nil
	}
	if err != nil {
		return avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, diam.UnableToComply), err
	}
	if !hasPublicIdentity(msg) {
		return h.fail(msg, DiameterErrorOperationNotAllowed), nil
	}

	rd, err := parseRepositoryUpdate(msg)
	if err != nil {
		var re resultError
		if errors.As(err, &re) {
			h.log.Warn("sh: PUR refused", zap.String("identity", identity), zap.Error(err))
			return h.fail(msg, uint32(re)), nil
		}
		return nil, err
	}

	// 5 and 6: the sequence number rules of TS 29.328 §6.1.2, under a row lock.
	seq := *rd.SequenceNumber
	var data *string
	if rd.ServiceData != nil {
		data = &rd.ServiceData.Inner
	}
	err = h.repository.UpdateShRepositoryData(ctx, sub.IMSSubscriberID, rd.ServiceIndication,
		func(cur *models.ShRepositoryData) (*models.ShRepositoryData, error) {
			if cur == nil {
				if seq != 0 {
					return nil, resultError(DiameterErrorTransparentDataOutOfSync)
				}
				if data == nil {
					return nil, resultError(DiameterErrorOperationNotAllowed)
				}
				return &models.ShRepositoryData{SequenceNumber: 0, ServiceData: *data}, nil
			}
			if seq == 0 || seq-1 != cur.SequenceNumber%65535 {
				return nil, resultError(DiameterErrorTransparentDataOutOfSync)
			}
			if data == nil {
				return nil, nil
			}
			return &models.ShRepositoryData{SequenceNumber: seq, ServiceData: *data}, nil
		})
	var re resultError
	switch {
	case errors.As(err, &re):
		h.log.Warn("sh: PUR refused", zap.String("identity", identity), zap.String("service_indication", rd.ServiceIndication),
			zap.Int("sequence_number", seq), zap.Uint32("experimental_result", uint32(re)))
		return h.fail(msg, uint32(re)), nil
	case errors.Is(err, repository.ErrConflict):
		return h.fail(msg, DiameterPriorUpdateInProgress), nil
	case err != nil:
		return avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, diam.UnableToComply), err
	}

	h.log.Info("sh: repository data updated", zap.String("identity", identity),
		zap.String("service_indication", rd.ServiceIndication), zap.Int("sequence_number", seq), zap.Bool("removed", data == nil))
	ans := avputil.ConstructSuccessAnswer(msg, sessionID(msg), h.originHost, h.originRealm, AppIDSh)
	addVendorSpecificApplicationID(ans)
	ans.NewAVP(avpRepositoryDataID, avp.Vbit, Vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpServiceIndication, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(rd.ServiceIndication)),
		diam.NewAVP(avpSequenceNumber, avp.Vbit, Vendor3GPP, datatype.Unsigned32(seq)),
	}})
	return ans, nil
}

// parseRepositoryUpdate reads the one RepositoryData of a PUR's User-Data.
func parseRepositoryUpdate(msg *diam.Message) (*repositoryDataElem, error) {
	ud, err := msg.FindAVP(avpUserData, Vendor3GPP)
	if err != nil {
		return nil, resultError(DiameterErrorUserDataNotRecognized)
	}
	raw, _ := ud.Data.(datatype.OctetString)
	if len(raw) > MaxServiceData+4096 {
		return nil, resultError(DiameterErrorTooMuchData)
	}
	var doc shData
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, resultError(DiameterErrorUserDataNotRecognized)
	}
	if len(doc.RepositoryData) != 1 {
		if len(doc.RepositoryData) > 1 {
			// Several instances need Update-Eff.
			return nil, resultError(DiameterErrorOperationNotAllowed)
		}
		return nil, resultError(DiameterErrorUserDataNotRecognized)
	}
	rd := doc.RepositoryData[0]
	rd.ServiceIndication = strings.TrimSpace(rd.ServiceIndication)
	if rd.ServiceIndication == "" || len(rd.ServiceIndication) > 255 || rd.SequenceNumber == nil ||
		*rd.SequenceNumber < 0 || *rd.SequenceNumber > 65535 {
		return nil, resultError(DiameterErrorUserDataNotRecognized)
	}
	if rd.ServiceData != nil && len(rd.ServiceData.Inner) > MaxServiceData {
		return nil, resultError(DiameterErrorTooMuchData)
	}
	return &rd, nil
}

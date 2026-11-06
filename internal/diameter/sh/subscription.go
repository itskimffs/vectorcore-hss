// Added by itskimffs (2026-11), see NOTICE: Sh subscriptions to repository data.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package sh

// Sh-Subs-Notif and Sh-Notif for repository data, 3GPP TS 29.328 §6.1.3 and
// §6.1.4 (TS 29.329 §6.1.5 to §6.1.8). An Application Server on the
// repository data permission list subscribes with an SNR to the data of a
// public identity and Service-Indication. When the data changes, by an Sh
// PUR from another AS or in the API and web UI, the HSS sends it a PNR with
// the new data. Removed data is a RepositoryData with Sequence-Number 0 and no
// ServiceData.
//
// Subscriptions are kept in memory and are lost when the HSS restarts. Each
// lasts at most maxSubscription; the SNA says how long in Expiry-Time, and the
// AS subscribes again before then. Only RepositoryData can be subscribed to.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"go.uber.org/zap"

	"github.com/svinson1121/vectorcore-hss/internal/diameter/avputil"
	"github.com/svinson1121/vectorcore-hss/internal/models"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
)

const (
	cmdSNR = uint32(308)

	avpSubsReqType        = uint32(705)
	avpExpiryTime         = uint32(709)
	avpSendDataIndication = uint32(710)

	subsReqTypeSubscribe   = 0
	subsReqTypeUnsubscribe = 1
	userDataRequested      = 1 // Send-Data-Indication

	// TS 29.329 §6.2.2
	DiameterErrorUserDataCannotBeNotified = uint32(5104)
	DiameterErrorSubsDataAbsent           = uint32(5106)

	// maxSubscription is the longest subscription the HSS grants.
	maxSubscription = 24 * time.Hour
)

type subscriptionKey struct {
	imsSubscriberID   int
	serviceIndication string
}

// subscriber is one AS's subscription to one identity's data.
type subscriber struct {
	host, realm, identity string
	expiry                time.Time
}

// subscriptions are the Sh subscriptions to repository data.
type subscriptions struct {
	mu sync.Mutex
	m  map[subscriptionKey][]subscriber
}

// set adds or renews s. It replaces a subscription by the same AS to the same
// identity.
func (ss *subscriptions) set(k subscriptionKey, s subscriber) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.m == nil {
		ss.m = map[subscriptionKey][]subscriber{}
	}
	list := ss.m[k][:0:0]
	for _, o := range ss.m[k] {
		if !o.same(s) && o.expiry.After(time.Now()) {
			list = append(list, o)
		}
	}
	ss.m[k] = append(list, s)
}

// remove ends s and reports whether there was one.
func (ss *subscriptions) remove(k subscriptionKey, s subscriber) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	found := false
	var list []subscriber
	for _, o := range ss.m[k] {
		switch {
		case o.same(s):
			found = true
		case o.expiry.After(time.Now()):
			list = append(list, o)
		}
	}
	if len(list) == 0 {
		delete(ss.m, k)
	} else {
		ss.m[k] = list
	}
	return found
}

// current returns the subscriptions that have not expired, and forgets the
// others.
func (ss *subscriptions) current(k subscriptionKey) []subscriber {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var list []subscriber
	for _, o := range ss.m[k] {
		if o.expiry.After(time.Now()) {
			list = append(list, o)
		}
	}
	if len(list) == 0 {
		delete(ss.m, k)
	} else {
		ss.m[k] = list
	}
	return list
}

func (s subscriber) same(o subscriber) bool {
	return strings.EqualFold(s.host, o.host) && s.identity == o.identity
}

func diameterIdentity(msg *diam.Message, code uint32) string {
	a, err := msg.FindAVP(code, 0)
	if err != nil {
		return ""
	}
	id, _ := a.Data.(datatype.DiameterIdentity)
	return strings.TrimSuffix(string(id), ".")
}

func enumerated(msg *diam.Message, code uint32) (int32, bool) {
	a, err := msg.FindAVP(code, Vendor3GPP)
	if err != nil {
		return 0, false
	}
	e, ok := a.Data.(datatype.Enumerated)
	return int32(e), ok
}

// publicIdentityOf returns the Public-Identity in User-Identity.
func publicIdentityOf(msg *diam.Message) string {
	ui, err := msg.FindAVP(avpUserIdentity, Vendor3GPP)
	if err != nil {
		return ""
	}
	g, ok := ui.Data.(*diam.GroupedAVP)
	if !ok {
		return ""
	}
	for _, a := range g.AVP {
		if s, ok := a.Data.(datatype.UTF8String); ok && a.Code == avpPublicIdentity {
			return string(s)
		}
	}
	return ""
}

// SNR handles Sh Subscribe-Notifications-Request (command 308), for
// repository data only.
func (h *Handlers) SNR(_ diam.Conn, msg *diam.Message) (*diam.Message, error) {
	refs, _ := msg.FindAVPs(avpDataReference, Vendor3GPP)
	if len(refs) == 0 {
		return h.fail(msg, DiameterErrorUserDataNotRecognized), nil
	}
	for _, r := range refs {
		if ref, ok := r.Data.(datatype.Enumerated); !ok || int32(ref) != DataReferenceRepositoryData {
			return h.fail(msg, DiameterErrorUserDataCannotBeNotified), nil
		}
	}
	if h.repository == nil || !h.mayUseRepositoryData(msg) {
		return h.fail(msg, DiameterErrorUserDataCannotBeNotified), nil
	}
	reqType, ok := enumerated(msg, avpSubsReqType)
	if !ok || (reqType != subsReqTypeSubscribe && reqType != subsReqTypeUnsubscribe) {
		return h.fail(msg, DiameterErrorUserDataNotRecognized), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identity := publicIdentityOf(msg)
	if identity == "" {
		if !hasPublicIdentity(msg) {
			return h.fail(msg, DiameterErrorOperationNotAllowed), nil
		}
		return h.fail(msg, DiameterErrorUserUnknown), nil
	}
	sub, err := h.lookupIMSSubscriber(ctx, identity)
	if errors.Is(err, repository.ErrNotFound) {
		return h.fail(msg, DiameterErrorUserUnknown), nil
	}
	if err != nil {
		return avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, diam.UnableToComply), err
	}
	indications := serviceIndications(msg)
	if len(indications) == 0 {
		return h.fail(msg, DiameterErrorUserDataNotRecognized), nil
	}

	as := subscriber{host: diameterIdentity(msg, avp.OriginHost), realm: diameterIdentity(msg, avp.OriginRealm), identity: identity}
	ans := avputil.ConstructSuccessAnswer(msg, sessionID(msg), h.originHost, h.originRealm, AppIDSh)
	addVendorSpecificApplicationID(ans)
	if reqType == subsReqTypeUnsubscribe {
		found := false
		for _, si := range indications {
			found = h.subscriptions.remove(subscriptionKey{sub.IMSSubscriberID, si}, as) || found
		}
		if !found {
			return h.fail(msg, DiameterErrorSubsDataAbsent), nil
		}
		h.log.Info("sh: unsubscribed from repository data", zap.String("identity", identity), zap.String("as", as.host),
			zap.Strings("service_indications", indications))
		return ans, nil
	}

	as.expiry = time.Now().Add(maxSubscription).Truncate(time.Second)
	if a, err := msg.FindAVP(avpExpiryTime, Vendor3GPP); err == nil {
		if t, ok := a.Data.(datatype.Time); ok && time.Time(t).Before(as.expiry) {
			as.expiry = time.Time(t)
		}
	}
	if !as.expiry.After(time.Now()) {
		return h.fail(msg, DiameterErrorUserDataNotRecognized), nil
	}
	if sdi, ok := enumerated(msg, avpSendDataIndication); ok && sdi == userDataRequested {
		rows, err := h.repository.GetShRepositoryData(ctx, sub.IMSSubscriberID, indications)
		if err != nil {
			return avputil.ConstructFailureAnswer(msg, sessionID(msg), h.originHost, h.originRealm, diam.UnableToComply), err
		}
		if len(rows) > 0 {
			ans.NewAVP(avpUserData, avp.Mbit|avp.Vbit, Vendor3GPP, datatype.OctetString(repositoryDataXML(indications, rows)))
		}
	}
	for _, si := range indications {
		h.subscriptions.set(subscriptionKey{sub.IMSSubscriberID, si}, as)
	}
	ans.NewAVP(avpExpiryTime, avp.Vbit, Vendor3GPP, datatype.Time(as.expiry))
	h.log.Info("sh: subscribed to repository data", zap.String("identity", identity), zap.String("as", as.host),
		zap.Strings("service_indications", indications), zap.Time("expiry", as.expiry))
	return ans, nil
}

// NotifyRepositoryData sends the current repository data of an IMS subscriber
// for one Service-Indication in a PNR to each AS that has subscribed to it,
// except the AS named by except: the one that made the change knows it
// already. Call it after the change is stored.
func (h *Handlers) NotifyRepositoryData(imsSubscriberID int, serviceIndication, except string) {
	list := h.subscriptions.current(subscriptionKey{imsSubscriberID, serviceIndication})
	var to []subscriber
	for _, s := range list {
		if !strings.EqualFold(s.host, strings.TrimSuffix(except, ".")) {
			to = append(to, s)
		}
	}
	if len(to) == 0 || h.repository == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := h.repository.GetShRepositoryData(ctx, imsSubscriberID, []string{serviceIndication})
	if err != nil {
		h.log.Error("sh: no PNR, the repository data cannot be read", zap.Int("ims_subscriber_id", imsSubscriberID),
			zap.String("service_indication", serviceIndication), zap.Error(err))
		return
	}
	var stored []models.ShRepositoryData
	for _, r := range rows {
		if r.ServiceIndication == serviceIndication {
			stored = append(stored, r)
		}
	}
	data := repositoryDataXML([]string{serviceIndication}, stored)
	for _, s := range to {
		h.sendPNR(s.identity, s.host, s.realm, data)
	}
}

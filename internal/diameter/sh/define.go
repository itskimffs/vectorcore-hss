package sh

import "github.com/fiorix/go-diameter/v4/diam/datatype"

const (
	AppIDSh    = uint32(16777217)
	Vendor3GPP = uint32(10415)

	DiameterErrorUserUnknown          = uint32(5001)
	DiameterErrorUserDataNotAvailable = uint32(5007) // subscriber exists but requested data not stored
	DiameterErrorNotSupportedUserData = uint32(5009) // Data-Reference value not supported by HSS

	// TS 29.329 §6.2
	DiameterErrorTooMuchData              = uint32(5008)
	DiameterErrorUserDataNotRecognized    = uint32(5100)
	DiameterErrorOperationNotAllowed      = uint32(5101)
	DiameterErrorUserDataCannotBeRead     = uint32(5102)
	DiameterErrorUserDataCannotBeModified = uint32(5103)
	DiameterErrorTransparentDataOutOfSync = uint32(5105)
	DiameterPriorUpdateInProgress         = uint32(4101)

	// Data-Reference values (3GPP TS 29.328 §6.3.2)
	DataReferenceRepositoryData        = int32(0)
	DataReferenceIMSPublicIdentity     = int32(10)
	DataReferenceIMSUserState          = int32(11)
	DataReferenceSCSCFName             = int32(12)
	DataReferenceInitialFilterCriteria = int32(13)
	DataReferenceLocationInformation   = int32(14)
	DataReferenceUserState             = int32(15)
	DataReferenceMSISDN                = int32(17)

	avpUserIdentity   = uint32(700)
	avpMSISDN         = uint32(701)
	avpUserData       = uint32(702) // Sh-User-Data (TS 29.329); NOT 606 which is Cx-User-Data (TS 29.229)
	avpDataReference  = uint32(703)
	avpPublicIdentity = uint32(601)

	avpServiceIndication = uint32(704)
	avpRepositoryDataID  = uint32(715)
	avpSequenceNumber    = uint32(716)
)

type UserIdentity struct {
	PublicIdentity datatype.UTF8String  `avp:"Public-Identity,omitempty"`
	MSISDN         datatype.OctetString `avp:"MSISDN,omitempty"`
}

type UDR struct {
	SessionID        datatype.UTF8String       `avp:"Session-Id"`
	OriginHost       datatype.DiameterIdentity `avp:"Origin-Host,omitempty"`
	OriginRealm      datatype.DiameterIdentity `avp:"Origin-Realm,omitempty"`
	UserIdentity     *UserIdentity             `avp:"User-Identity,omitempty"`
	DataReference    datatype.Enumerated       `avp:"Data-Reference,omitempty"`
	AuthSessionState int32                     `avp:"Auth-Session-State,omitempty"`
}

package zh

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// AppIDZh is the 3GPP Zh Diameter application ID (3GPP TS 29.109).
const AppIDZh = uint32(16777221)

// LoadDict registers the Zh application in the global dictionary.
// The command needs its request and answer rules: go-diameter cannot read a
// message whose command has none, and drops the connection. The SIP AVPs and
// Public-Identity are declared here as well, since go-diameter looks an AVP up
// in the application of the message and then only in the base application.
func LoadDict() error {
	if err := dict.Default.Load(strings.NewReader(dictXML)); err != nil {
		return fmt.Errorf("zh: load dict: %w", err)
	}
	return nil
}

const dictXML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
  <application id="16777221" type="auth" name="Zh">
    <vendor id="10415" name="3GPP"/>

    <!-- 3GPP TS 29.109 6.1.1 and 6.1.2 -->
    <command code="303" short="MA" name="Multimedia-Auth">
      <request>
        <rule avp="Session-Id" required="true" max="1"/>
        <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
        <rule avp="Auth-Session-State" required="true" max="1"/>
        <rule avp="Origin-Host" required="true" max="1"/>
        <rule avp="Origin-Realm" required="true" max="1"/>
        <rule avp="Destination-Realm" required="true" max="1"/>
        <rule avp="Destination-Host" required="false" max="1"/>
        <rule avp="User-Name" required="false" max="1"/>
        <rule avp="Public-Identity" required="false" max="1"/>
        <rule avp="SIP-Number-Auth-Items" required="false" max="1"/>
        <rule avp="SIP-Auth-Data-Item" required="false" max="1"/>
        <rule avp="Proxy-Info" required="false"/>
        <rule avp="Route-Record" required="false"/>
      </request>
      <answer>
        <rule avp="Session-Id" required="true" max="1"/>
        <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
        <rule avp="Result-Code" required="false" max="1"/>
        <rule avp="Experimental-Result" required="false" max="1"/>
        <rule avp="Auth-Session-State" required="false" max="1"/>
        <rule avp="Origin-Host" required="true" max="1"/>
        <rule avp="Origin-Realm" required="true" max="1"/>
        <rule avp="User-Name" required="false" max="1"/>
        <rule avp="Public-Identity" required="false" max="1"/>
        <rule avp="SIP-Number-Auth-Items" required="false" max="1"/>
        <rule avp="SIP-Auth-Data-Item" required="false"/>
        <rule avp="Proxy-Info" required="false"/>
        <rule avp="Route-Record" required="false"/>
      </answer>
    </command>

    <!-- go-diameter looks up an AVP in the application of the message and then in the base, so the Cx definitions do not reach Zh. These are copied from the Cx dictionary. -->
    <avp name="Public-Identity" code="601" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="UTF8String"/>
    </avp>
    <avp name="SIP-Number-Auth-Items" code="607" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="Unsigned32"/>
    </avp>
    <avp name="SIP-Authentication-Scheme" code="608" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="UTF8String"/>
    </avp>
    <avp name="SIP-Authenticate" code="609" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="OctetString"/>
    </avp>
    <avp name="SIP-Authorization" code="610" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="OctetString"/>
    </avp>
    <avp name="SIP-Authentication-Context" code="611" must="V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="OctetString"/>
    </avp>
    <avp name="SIP-Auth-Data-Item" code="612" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="Grouped">
        <rule avp="SIP-Item-Number" required="false" max="1"/>
        <rule avp="SIP-Authentication-Scheme" required="false" max="1"/>
        <rule avp="SIP-Authenticate" required="false" max="1"/>
        <rule avp="SIP-Authorization" required="false" max="1"/>
        <rule avp="SIP-Authentication-Context" required="false" max="1"/>
        <rule avp="Confidentiality-Key" required="false" max="1"/>
        <rule avp="Integrity-Key" required="false" max="1"/>
      </data>
    </avp>
    <avp name="SIP-Item-Number" code="613" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="Unsigned32"/>
    </avp>
    <avp name="Confidentiality-Key" code="625" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="OctetString"/>
    </avp>
    <avp name="Integrity-Key" code="626" must="M,V" must-not="-" may-encrypt="N" vendor-id="10415">
      <data type="OctetString"/>
    </avp>
  </application>
</diameter>`

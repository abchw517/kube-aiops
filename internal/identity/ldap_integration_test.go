package identity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

func TestAuthenticateLDAPOverTLS(t *testing.T) {
	const (
		baseDN  = "dc=example,dc=com"
		bindDN  = "cn=reader," + baseDN
		userDN  = "CN=Alice," + baseDN
		groupDN = "CN=Readers," + baseDN
		filter  = "(&(objectCategory=person)(objectClass=user)(sAMAccountName=%s))"
	)
	certSource := httptest.NewTLSServer(nil)
	defer certSource.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: certSource.TLS.Certificates,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				for {
					packet, err := ber.ReadPacket(conn)
					if err != nil {
						return
					}
					id, request := packet.Children[0], packet.Children[1]
					switch request.Tag {
					case ldap.ApplicationBindRequest:
						dn := request.Children[1].Value
						password := request.Children[2].Data.String()
						code := ldap.LDAPResultInvalidCredentials
						if (dn == bindDN && password == "fixture-bind") || (dn == userDN && password == "fixture-user") {
							code = ldap.LDAPResultSuccess
						}
						writeLDAPResponse(conn, id, ldapResult(ldap.ApplicationBindResponse, code))
					case ldap.ApplicationSearchRequest:
						actual, err := ldap.DecompileFilter(request.Children[6])
						if err != nil || (actual != "(&(objectCategory=person)(objectClass=user)(sAMAccountName=alice))" && actual != "(objectClass=*)" && actual != "(sAMAccountName=alice)") {
							writeLDAPResponse(conn, id, ldapResult(ldap.ApplicationSearchResultDone, ldap.LDAPResultSuccess))
							continue
						}
						entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "")
						entry.AppendChild(ldapString(userDN))
						attrs, attr := ber.NewSequence(""), ber.NewSequence("")
						attr.AppendChild(ldapString("memberOf"))
						values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "")
						values.AppendChild(ldapString(groupDN))
						attr.AppendChild(values)
						attrs.AppendChild(attr)
						entry.AppendChild(attrs)
						writeLDAPResponse(conn, id, entry)
						writeLDAPResponse(conn, id, ldapResult(ldap.ApplicationSearchResultDone, ldap.LDAPResultSuccess))
					default:
						return
					}
				}
			}()
		}
	}()
	defer func() { listener.Close(); <-finished }()

	roots := x509.NewCertPool()
	roots.AddCert(certSource.Certificate())
	cfg := LDAPConfig{
		URL: "ldaps://" + listener.Addr().String(), BaseDN: baseDN,
		BindDN: bindDN, BindPassword: "fixture-bind", UserFilter: filter, GroupAttr: "memberOf",
		TLSConfig: &tls.Config{RootCAs: roots},
	}
	if stage := TestLDAP(t.Context(), cfg); stage != "" {
		t.Fatalf("LDAP test returned %q", stage)
	}
	invalid := cfg
	invalid.BindPassword = "incorrect"
	if stage := TestLDAP(t.Context(), invalid); stage != "bind" {
		t.Fatalf("invalid bind password returned %q", stage)
	}
	got, err := AuthenticateLDAP(cfg, "alice", "fixture-user")
	if err != nil || got.Subject != "ldap:cn=alice,"+baseDN || !slices.Equal(got.Groups, []string{"ldap:cn=readers," + baseDN}) {
		t.Fatalf("LDAPS result = %#v, %v", got, err)
	}
	shorthand := cfg
	shorthand.UserFilter = "sAMAccountName"
	if _, err := AuthenticateLDAP(shorthand, "alice", "fixture-user"); err != nil {
		t.Fatalf("LDAP attribute shorthand: %v", err)
	}
	if _, err := AuthenticateLDAP(cfg, "alice", "incorrect"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("bad password: %v", err)
	}
	cfg.TLSConfig = nil
	if _, err := AuthenticateLDAP(cfg, "alice", "fixture-user"); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("untrusted CA: %v", err)
	}
}

func ldapString(value string) *ber.Packet {
	return ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "")
}

func ldapResult(tag ber.Tag, code int) *ber.Packet {
	result := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "")
	result.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, ""))
	result.AppendChild(ldapString(""))
	result.AppendChild(ldapString(""))
	return result
}

func writeLDAPResponse(conn net.Conn, id, payload *ber.Packet) {
	response := ber.NewSequence("")
	response.AppendChild(id)
	response.AppendChild(payload)
	_, _ = conn.Write(response.Bytes())
}

func TestLDAPStartTLSRefusalStopsBeforeBind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan bool, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- false
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		packet, err := ber.ReadPacket(conn)
		if err != nil || packet.Children[1].Tag != ldap.ApplicationExtendedRequest {
			finished <- false
			return
		}
		writeLDAPResponse(conn, packet.Children[0], ldapResult(ldap.ApplicationExtendedResponse, ldap.LDAPResultUnavailable))
		_, err = ber.ReadPacket(conn)
		finished <- errors.Is(err, io.EOF)
	}()
	cfg := LDAPConfig{URL: "ldap://" + listener.Addr().String(), BaseDN: "dc=example",
		BindDN: "cn=reader,dc=example", BindPassword: "fixture-bind", UserUID: "sAMAccountName",
		UserFilter: "(objectClass=user)", SearchScope: "onelevel"}
	if got := cfg.filterTemplate(); got != "(&(sAMAccountName=%s)(objectClass=user))" {
		t.Fatalf("unexpected user filter: %s", got)
	}
	if stage := TestLDAP(t.Context(), cfg); stage != "connection" {
		t.Fatalf("unexpected test stage: %s", stage)
	}
	if !<-finished {
		t.Fatal("expected StartTLS and connection close without a bind")
	}
}

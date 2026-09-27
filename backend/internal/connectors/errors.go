package connectors

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"

	"omnidatabus/internal/netguard"
	"omnidatabus/internal/problem"
)

// Kind classifies a connection failure so the UI can explain it in plain words.
type Kind string

// Failure kinds.
const (
	KindUnreachable      Kind = "unreachable"
	KindDNS              Kind = "dns"
	KindTimeout          Kind = "timeout"
	KindTLS              Kind = "tls"
	KindAuth             Kind = "auth"
	KindDatabaseNotFound Kind = "database_not_found"
	KindPermission       Kind = "permission"
	KindUnknown          Kind = "unknown"
	// KindEgressDenied: the server's egress policy refused the destination (OC-033).
	KindEgressDenied Kind = "egress_denied"
)

// ConnError is a classified connector error. The raw driver text is kept as Detail.
type ConnError struct {
	Role  Role
	Kind  Kind
	Cause error
}

func (e *ConnError) Error() string { return string(e.Kind) + ": " + e.Cause.Error() }
func (e *ConnError) Unwrap() error { return e.Cause }

var kindInfo = map[Kind]struct {
	code, title, hint string
	status            int
}{
	KindUnreachable:      {"UNREACHABLE", "Host unreachable", "Check the host and port, and that this server can reach the database (firewalls, security groups, allowed IPs).", http.StatusBadGateway},
	KindDNS:              {"DNS", "Host not found", "The host name didn't resolve. Check for typos.", http.StatusBadGateway},
	KindTimeout:          {"TIMEOUT", "Connection timed out", "The database didn't answer in time. Check network access and allowed IPs.", http.StatusGatewayTimeout},
	KindTLS:              {"TLS", "TLS handshake failed", "Check the SSL mode and the CA certificate.", http.StatusBadGateway},
	KindAuth:             {"AUTH_FAILED", "Authentication failed", "Check the username and password.", http.StatusUnprocessableEntity},
	KindDatabaseNotFound: {"DB_NOT_FOUND", "Database not found", "Check the database name.", http.StatusUnprocessableEntity},
	KindPermission:       {"PERMISSION_DENIED", "Permission denied", "The user lacks a required privilege.", http.StatusUnprocessableEntity},
	KindUnknown:          {"UNKNOWN", "Connection failed", "See the details for the database's message.", http.StatusBadGateway},
	KindEgressDenied:     {netguard.CodeEgressDenied, "Destination not allowed", netguard.ErrDenied.Error(), http.StatusUnprocessableEntity},
}

// Code is the stable problem code, e.g. SOURCE_AUTH_FAILED.
func (e *ConnError) Code() string {
	if e.Kind == KindEgressDenied {
		return netguard.CodeEgressDenied // the same for sources and targets
	}
	return strings.ToUpper(string(e.Role)) + "_" + kindInfo[e.Kind].code
}

// Hint is a user-facing suggestion.
func (e *ConnError) Hint() string { return kindInfo[e.Kind].hint }

// Problem implements problem.Problemer.
func (e *ConnError) Problem() problem.Problem {
	k := kindInfo[e.Kind]
	if e.Kind == KindEgressDenied {
		return problem.New(k.status, e.Code(), k.title, e.Hint()) // never echo what the host resolved to
	}
	p := problem.New(k.status, e.Code(), k.title, e.Hint()+" ("+e.Cause.Error()+")")
	return p
}

// sqlStater is implemented by driver errors carrying a SQLSTATE (e.g. *pgconn.PgError).
type sqlStater interface{ SQLState() string }

// Classify wraps err with its Kind. Driver-specific errors can be classified by the connector
// first; this handles network, TLS, context and SQLSTATE errors generically.
func Classify(role Role, err error) *ConnError {
	var ce *ConnError
	if errors.As(err, &ce) {
		return ce
	}
	return &ConnError{Role: role, Kind: kindOf(err), Cause: err}
}

func kindOf(err error) Kind {
	var dnsErr *net.DNSError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	var st sqlStater
	switch {
	case netguard.IsDenied(err):
		return KindEgressDenied
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.As(err, &dnsErr):
		return KindDNS
	case errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &certInvalid):
		return KindTLS
	case errors.As(err, &st):
		return kindFromSQLState(st.SQLState())
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return KindUnreachable
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "tls") || strings.Contains(msg, "x509") || strings.Contains(msg, "certificate"):
		return KindTLS
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return KindTimeout
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no route to host"):
		return KindUnreachable
	}
	return KindUnknown
}

func kindFromSQLState(code string) Kind {
	switch {
	case code == "28P01" || code == "28000":
		return KindAuth
	case code == "3D000":
		return KindDatabaseNotFound
	case code == "42501":
		return KindPermission
	case strings.HasPrefix(code, "08"):
		return KindUnreachable
	case code == "57014":
		return KindTimeout
	}
	return KindUnknown
}

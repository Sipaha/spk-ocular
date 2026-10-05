package provider

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// InvalidTLS identifies local certificate/configuration failures. Repeating a
// connection cannot fix them; callers must keep certificate verification intact.
func InvalidTLS(err error) bool {
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	return errors.As(err, &verification) || errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &certificate) || errors.As(err, &record)
}

package connections

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	m_rand "math/rand"
	"net"
	"time"
)

// let's make our own certs
//
//	"credit: https://gist.github.com/shaneutt/5e1995295cff6721c89a71d13a71c251"
func certification() (*tls.Config, *tls.Config, error) {
	certificate_authority := &x509.Certificate{
		SerialNumber: big.NewInt(int64(m_rand.Int())),
		Subject: pkix.Name{
			Organization:  []string{"simple-gnomon"},
			Country:       []string{"DERO"},
			Province:      []string{"NETWORK1"},
			Locality:      []string{"MAINNET"},
			StreetAddress: []string{"1337 Street"},
			PostalCode:    []string{"00000"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add((time.Hour * 24) * 365),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	certificate_authority_priv_key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	certificate_authority_bytes, err := x509.CreateCertificate(
		rand.Reader,
		certificate_authority,
		certificate_authority,
		&certificate_authority_priv_key.PublicKey,
		certificate_authority_priv_key,
	)
	if err != nil {
		return nil, nil, err
	}
	certificate_authority_pem := new(bytes.Buffer)
	pem.Encode(certificate_authority_pem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate_authority_bytes,
	})

	certificate_authority_priv_key_pem := new(bytes.Buffer)
	pem.Encode(certificate_authority_priv_key_pem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certificate_authority_priv_key),
	})

	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(int64(m_rand.Int())),
		Subject: pkix.Name{
			Organization:  []string{"simple-gnomon"},
			Country:       []string{"DERO"},
			Province:      []string{"NETWORK1"},
			Locality:      []string{"MAINNET"},
			StreetAddress: []string{"1337 Street"},
			PostalCode:    []string{"00000"},
		},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{{127, 0, 0, 1}, net.IPv6loopback},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add((time.Hour * 24) * 365),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	certificate_priv_key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	certificate_bytes, err := x509.CreateCertificate(
		rand.Reader,
		certificate,
		certificate_authority,
		&certificate_priv_key.PublicKey,
		certificate_authority_priv_key,
	)
	if err != nil {
		return nil, nil, err
	}

	certificate_pem := new(bytes.Buffer)
	pem.Encode(certificate_pem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate_bytes,
	})

	certificate_priv_key_pem := new(bytes.Buffer)
	pem.Encode(certificate_priv_key_pem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certificate_priv_key),
	})

	server_certificate, err := tls.X509KeyPair(certificate_pem.Bytes(), certificate_priv_key_pem.Bytes())
	if err != nil {
		return nil, nil, err
	}

	server_tls_config := &tls.Config{Certificates: []tls.Certificate{server_certificate}}

	certpool := x509.NewCertPool()
	certpool.AppendCertsFromPEM(certificate_authority_pem.Bytes())

	client_tls_config := &tls.Config{RootCAs: certpool, ServerName: "localhost"}

	return server_tls_config, client_tls_config, nil
}

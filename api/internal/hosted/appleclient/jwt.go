package appleclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ParseECPrivateKeyPEM parses an Apple .p8 key (PKCS#8) or a SEC1 EC key; it must be P-256.
// Errors never include the key material.
func ParseECPrivateKeyPEM(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key: no PEM block found")
	}
	var key *ecdsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key: not an EC key")
		}
		key = ec
	} else if ec, err2 := x509.ParseECPrivateKey(block.Bytes); err2 == nil {
		key = ec
	} else {
		return nil, errors.New("private key: cannot parse (want a PKCS#8 or SEC1 EC key)")
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("private key: curve must be P-256 (ES256)")
	}
	return key, nil
}

// LoadKeyFromEnv reads a PEM private key from $name (literal PEM, "\n" escapes allowed) or,
// when that is empty, from the file named by $name_FILE.
func LoadKeyFromEnv(name string) (*ecdsa.PrivateKey, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return ParseECPrivateKeyPEM([]byte(strings.ReplaceAll(v, `\n`, "\n")))
	}
	if path := strings.TrimSpace(os.Getenv(name + "_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s_FILE: cannot read the key file", name)
		}
		return ParseECPrivateKeyPEM(b)
	}
	return nil, fmt.Errorf("neither %s nor %s_FILE is set", name, name)
}

// signES256 builds a compact JWS: header and claims as JSON, signature r||s (RFC 7518).
func signES256(key *ecdsa.PrivateKey, header, claims map[string]any) (string, error) {
	header["alg"] = "ES256"
	header["typ"] = "JWT"
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

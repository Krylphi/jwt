package jwt

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strconv"
)

// FormatWithoutSign updates the Raw field and returns a new JWT, with only the
// first two parts. The third part should contain the signature, unless alg is
// "none".
func (c *Claims) FormatWithoutSign(alg string) (tokenWithoutSignature []byte, err error) {
	return c.newToken(alg, 0)
}

// ECDSASign updates the Raw field and returns a new JWT.
// The return is an AlgError when alg is not in ECDSAAlgs.
// The caller must use the correct key for the respective algorithm (P-256 for
// ES256, P-384 for ES384 and P-521 for ES512) or risk malformed token production.
func (c *Claims) ECDSASign(alg string, key *ecdsa.PrivateKey) (token []byte, err error) {
	hash, err := hashLookup(alg, ECDSAAlgs)
	if err != nil {
		return nil, err
	}
	digest := hash.New()

	// signature contains pair (r, s) as per RFC 7518, subsection 3.4
	paramLen := (key.Curve.Params().BitSize + 7) / 8
	token, err = c.newToken(alg, encoding.EncodedLen(paramLen*2))
	if err != nil {
		return nil, err
	}
	digest.Write(token)

	r, s, err := ecdsa.Sign(rand.Reader, key, digest.Sum(token[len(token):]))
	if err != nil {
		return nil, err
	}

	token = append(token, '.')
	sig := token[len(token):cap(token)]
	// serialize r and s, using sig as a buffer
	i := len(sig)
	for _, word := range s.Bits() {
		for bitCount := strconv.IntSize; bitCount > 0; bitCount -= 8 {
			i--
			sig[i] = byte(word)
			word >>= 8
		}
	}
	// i might have exceeded paramLen due to the word size
	i = len(sig) - paramLen
	for _, word := range r.Bits() {
		for bitCount := strconv.IntSize; bitCount > 0; bitCount -= 8 {
			i--
			sig[i] = byte(word)
			word >>= 8
		}
	}

	// encoder won't overhaul source space
	encoding.Encode(sig, sig[len(sig)-2*paramLen:])
	return token[:cap(token)], nil
}

// EdDSASign updates the Raw field and returns a new JWT.
func (c *Claims) EdDSASign(key ed25519.PrivateKey) (token []byte, err error) {
	token, err = c.newToken(EdDSA, encoding.EncodedLen(ed25519.SignatureSize))
	if err != nil {
		return nil, err
	}

	sig := ed25519.Sign(key, token)

	token = append(token, '.')
	encoding.Encode(token[len(token):cap(token)], sig)
	return token[:cap(token)], nil
}

// HMACSign updates the Raw field and returns a new JWT.
// The return is an AlgError when alg is not in HMACAlgs.
func (c *Claims) HMACSign(alg string, secret []byte) (token []byte, err error) {
	if len(secret) == 0 {
		return nil, errNoSecret
	}

	hash, err := hashLookup(alg, HMACAlgs)
	if err != nil {
		return nil, err
	}
	digest := hmac.New(hash.New, secret)

	token, err = c.newToken(alg, encoding.EncodedLen(digest.Size()))
	if err != nil {
		return nil, err
	}
	digest.Write(token)

	token = append(token, '.')
	// use tail as a buffer; encoder won't overhaul source space
	bufOffset := cap(token) - digest.Size()
	encoding.Encode(token[len(token):cap(token)], digest.Sum(token[bufOffset:bufOffset]))
	return token[:cap(token)], nil
}

// RSASign updates the Raw field and returns a new JWT.
// The return is an AlgError when alg is not in RSAAlgs.
func (c *Claims) RSASign(alg string, key *rsa.PrivateKey) (token []byte, err error) {
	hash, err := hashLookup(alg, RSAAlgs)
	if err != nil {
		return nil, err
	}
	digest := hash.New()

	token, err = c.newToken(alg, encoding.EncodedLen(key.Size()))
	if err != nil {
		return nil, err
	}
	digest.Write(token)

	var sig []byte
	// use signature space as a buffer while not set
	buf := token[len(token):]
	if alg != "" && alg[0] == 'P' {
		sig, err = rsa.SignPSS(rand.Reader, key, hash, digest.Sum(buf), nil)
	} else {
		sig, err = rsa.SignPKCS1v15(rand.Reader, key, hash, digest.Sum(buf))
	}
	if err != nil {
		return nil, err
	}

	token = append(token, '.')
	encoding.Encode(token[len(token):cap(token)], sig)
	return token[:cap(token)], nil
}

// NewToken returns a new JWT without the signature part.
func (c *Claims) newToken(alg string, encSigLen int) ([]byte, error) {
	// define Claims.Raw
	var payload interface{}
	if c.Set == nil {
		payload = &c.Registered
	} else {
		payload = c.Set

		// merge Registered
		if c.Issuer != "" {
			c.Set[issuer] = c.Issuer
		}
		if c.Subject != "" {
			c.Set[subject] = c.Subject
		}
		switch len(c.Audiences) {
		case 0:
			break
		case 1: // single string
			c.Set[audience] = c.Audiences[0]
		default:
			array := make([]interface{}, len(c.Audiences))
			for i, s := range c.Audiences {
				array[i] = s
			}
			c.Set[audience] = array
		}
		if c.Expires != nil {
			c.Set[expires] = float64(*c.Expires)
		}
		if c.NotBefore != nil {
			c.Set[notBefore] = float64(*c.NotBefore)
		}
		if c.Issued != nil {
			c.Set[issued] = float64(*c.Issued)
		}
		if c.ID != "" {
			c.Set[id] = c.ID
		}
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	c.Raw = json.RawMessage(bytes)

	// try fixed encoding
	if c.ExtraHeaders == nil && c.KeyID == "" {
		var fixed string
		switch alg {
		case EdDSA:
			fixed = "eyJhbGciOiJFZERTQSJ9."
		case ES256:
			fixed = "eyJhbGciOiJFUzI1NiJ9."
		case ES384:
			fixed = "eyJhbGciOiJFUzM4NCJ9."
		case ES512:
			fixed = "eyJhbGciOiJFUzUxMiJ9."
		case HS256:
			fixed = "eyJhbGciOiJIUzI1NiJ9."
		case HS384:
			fixed = "eyJhbGciOiJIUzM4NCJ9."
		case HS512:
			fixed = "eyJhbGciOiJIUzUxMiJ9."
		case PS256:
			fixed = "eyJhbGciOiJQUzI1NiJ9."
		case PS384:
			fixed = "eyJhbGciOiJQUzM4NCJ9."
		case PS512:
			fixed = "eyJhbGciOiJQUzUxMiJ9."
		case RS256:
			fixed = "eyJhbGciOiJSUzI1NiJ9."
		case RS384:
			fixed = "eyJhbGciOiJSUzM4NCJ9."
		case RS512:
			fixed = "eyJhbGciOiJSUzUxMiJ9."
		}

		if fixed != "" {
			l := len(fixed) + encoding.EncodedLen(len(c.Raw))
			token := make([]byte, l, l+1+encSigLen)
			copy(token, fixed)
			encoding.Encode(token[len(fixed):], c.Raw)
			return token, nil
		}
	}

	var headerJSON []byte
	if c.ExtraHeaders != nil {
		c.ExtraHeaders["alg"] = alg
		if c.KeyID != "" {
			c.ExtraHeaders["kid"] = c.KeyID
		}
		var err error
		headerJSON, err = json.Marshal(c.ExtraHeaders)
		if err != nil {
			return nil, err
		}
	} else {
		if c.KeyID == "" {
			headerJSON = make([]byte, 0, 10+len(alg))
			headerJSON = append(headerJSON, `{"alg":`...)
		} else {
			headerJSON = make([]byte, 0, 19+len(c.KeyID)+len(alg))
			headerJSON = append(headerJSON, `{"kid":`...)
			headerJSON = strconv.AppendQuote(headerJSON, c.KeyID)
			headerJSON = append(headerJSON, `,"alg":`...)
		}
		headerJSON = strconv.AppendQuote(headerJSON, alg)
		headerJSON = append(headerJSON, '}')
	}

	headerLen := encoding.EncodedLen(len(headerJSON))
	l := headerLen + 1 + encoding.EncodedLen(len(c.Raw))
	token := make([]byte, l, l+1+encSigLen)

	encoding.Encode(token, headerJSON)
	token[headerLen] = '.'
	encoding.Encode(token[headerLen+1:], c.Raw)

	return token, nil
}

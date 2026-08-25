package qoder

import "encoding/base64"

// EncodeRequestBody encodes a request body using the Qoder WAF bypass scheme.
// The encoded body must be sent with &Encode=1 on the URL.
func EncodeRequestBody(plaintext []byte) string {
	std := base64.StdEncoding.EncodeToString(plaintext)
	n := len(std)
	a := n / 3
	rearranged := std[n-a:] + std[a:n-a] + std[:a]
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		c := rearranged[i]
		if int(c) < 128 && qoderS2C[c] >= 0 {
			out[i] = byte(qoderS2C[c])
		} else {
			out[i] = c
		}
	}
	return string(out)
}

const (
	qoderCustomAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	qoderStdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

var qoderS2C [128]int

func init() {
	for i := range qoderS2C {
		qoderS2C[i] = -1
	}
	for i := 0; i < 64; i++ {
		qoderS2C[qoderStdAlphabet[i]] = int(qoderCustomAlphabet[i])
	}
	qoderS2C['='] = int('$')
}

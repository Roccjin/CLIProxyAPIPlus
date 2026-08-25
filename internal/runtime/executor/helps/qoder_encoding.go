package helps

import qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"

// QoderEncodeBody encodes a request body using the Qoder body encoding scheme.
// This is a port of qoder2api's QoderEncoding.java. The algorithm:
//
//  1. Standard base64-encode the plaintext bytes.
//  2. Rearrange: split into thirds, reorder as [tail][mid][head].
//  3. Substitute each character using a custom alphabet mapping.
//
// The encoded body must be sent with &Encode=1 appended to the URL.
// The server decodes in reverse. This obfuscation prevents Alibaba Cloud WAF
// from pattern-matching the plaintext request body.
func QoderEncodeBody(plaintext []byte) string {
	return qoderauth.EncodeRequestBody(plaintext)
}

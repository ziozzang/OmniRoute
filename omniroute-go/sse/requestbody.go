package sse

// requestbody.go — Request-body resolution helpers.
// Port of src/sse/handlers/requestBody.ts
//
// Pure function — no race concerns.

// ResolveChatRequestBody prefers a pre-parsed body (avoids double-parsing large
// coding-agent payloads on the hot path, #4380). When preParsed is nil, the
// caller should parse from the request itself.
func ResolveChatRequestBody(preParsed any) (any, bool) {
	if preParsed != nil {
		return preParsed, true
	}
	return nil, false
}

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/human-agent65535/nassimhub-node/agent/modembackend/mock"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// rawAuthenticated issues a bearer-authenticated request whose body is the
// exact bytes given, not a re-encoded value.
func rawAuthenticated(n *node, token, method, path string, raw []byte) *http.Response {
	n.t.Helper()
	body := proto.WiFiConnectRequest{}
	return n.request(method, path, body, map[string]string{"Authorization": "Bearer " + token}, raw)
}

// A request body must be exactly one JSON document: trailing garbage, a
// second document or trailing bytes after the first value are rejected, the
// same contract the admin panel already enforces.
func TestRequestBodyWithTrailingGarbageIsRejected(t *testing.T) {
	n := newNode(t, mock.ChinaMobile)
	token := n.pair(newCore(t, "core-a"))

	for name, body := range map[string]string{
		"garbage after the object": `{"ssid":"HomeNet","psk":"secret-value"} trailing`,
		"second json document":     `{"ssid":"HomeNet","psk":"secret-value"}{"ssid":"Evil"}`,
		"trailing bytes no space":  `{"ssid":"HomeNet","psk":"secret-value"}x`,
		"only whitespace is fine":  "{\"ssid\":\"HomeNet\",\"psk\":\"secret-value\"}\n\t ",
		"single document is fine":  `{"ssid":"HomeNet","psk":"secret-value"}`,
	} {
		response := rawAuthenticated(n, token, "POST", "/v1/wifi/connect", []byte(body))
		accepted := name == "only whitespace is fine" || name == "single document is fine"
		// A decoded body may still be refused downstream (a second connect
		// returns 409); what must never happen for an accepted body is 400.
		if accepted && response.StatusCode == http.StatusBadRequest {
			t.Fatalf("%s: clean body was rejected as invalid JSON", name)
		}
		if !accepted && response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, response.StatusCode)
		}
		response.Body.Close()
	}
}

// Unknown fields stay rejected even when the body is otherwise a single
// document; the strictness stack is additive.
func TestUnknownFieldsStayRejected(t *testing.T) {
	n := newNode(t, mock.ChinaMobile)
	token := n.pair(newCore(t, "core-a"))

	response := rawAuthenticated(n, token, "POST", "/v1/wifi/connect", []byte(`{"ssid":"HomeNet","psk":"x","extra":1}`))
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	if code := errorBodyOf(t, response).Code; code != proto.ErrorInvalidArgument {
		t.Fatalf("error code = %s, want %s", code, proto.ErrorInvalidArgument)
	}
}

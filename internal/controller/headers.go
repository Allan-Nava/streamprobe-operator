// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// configError is a StreamProbe that cannot be checked as configured: Ready=False with reason
// InvalidConfig, and the stream is not contacted. Its message never carries a header value.
type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }

// A header name is an RFC 9110 token; a Secret key may hold characters a header cannot.
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// headersFromSecret turns a Secret into request headers: each key a header name, each value
// its value. A key that is not a header name, or a value with a line break (which would
// smuggle another header into the request), makes the whole Secret refused, naming the key
// and never the value.
func headersFromSecret(s *corev1.Secret) (http.Header, error) {
	if len(s.Data) == 0 && len(s.StringData) == 0 {
		return nil, &configError{fmt.Sprintf("headersFrom: Secret %q has no keys", s.Name)}
	}
	keys := slices.Sorted(maps.Keys(s.Data))
	h := http.Header{}
	for _, k := range keys {
		v := string(s.Data[k])
		if !headerName.MatchString(k) {
			return nil, &configError{fmt.Sprintf("headersFrom: Secret %q key %q is not a valid header name", s.Name, k)}
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, &configError{fmt.Sprintf("headersFrom: Secret %q key %q has a line break in its value", s.Name, k)}
		}
		h.Add(k, v)
	}
	return h, nil
}

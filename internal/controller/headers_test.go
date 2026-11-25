// SPDX-License-Identifier: MIT

package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHeadersFromSecret(t *testing.T) {
	secret := func(data map[string]string) *corev1.Secret {
		s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tok"}, Data: map[string][]byte{}}
		for k, v := range data {
			s.Data[k] = []byte(v)
		}
		return s
	}

	h, err := headersFromSecret(secret(map[string]string{"authorization": "Bearer x", "X-Origin-Key": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("Authorization") != "Bearer x" || h.Get("X-Origin-Key") != "k" {
		t.Errorf("got %v", h)
	}

	for name, data := range map[string]map[string]string{
		"empty":           {},
		"not a header":    {"bad key": "v"},
		"header smuggled": {"Authorization": "Bearer x\r\nX-Evil: 1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := headersFromSecret(secret(data))
			if _, ok := err.(*configError); !ok {
				t.Fatalf("want a configError, got %v", err)
			}
			if strings.Contains(err.Error(), "Bearer") {
				t.Errorf("the error carries the value: %v", err)
			}
		})
	}
}

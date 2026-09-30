package kubernetes

import (
	"errors"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func statusErr(st metav1.Status) error { return &apierrors.StatusError{ErrStatus: st} }

func TestSecretRefusalsNeverPrintServerStrings(t *testing.T) {
	const pin = "123"
	for name, err := range map[string]error{
		"message": statusErr(metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid, Message: "invalid PIN " + pin,
			Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid, Field: "data.pin", Message: pin}}}}),
		"a reason of the webhook's own": statusErr(metav1.Status{Code: 400, Reason: "invalid PIN " + pin, Message: "x"}),
		"a cause field and reason": statusErr(metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid,
			Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{Type: "PIN " + pin, Field: pin}, {Type: metav1.CauseTypeFieldValueRequired, Field: "x" + pin}}}}),
		"not a status": errors.New("webhook said " + pin),
	} {
		t.Run(name, func(t *testing.T) {
			m := secretSafe(err)
			for _, s := range append([]string{m.Text}, paramValues(m)...) {
				if strings.Contains(s, pin) {
					t.Fatalf("server text leaked: %q", s)
				}
			}
			if !strings.Contains(m.Text, "hidden") {
				t.Fatalf("no hidden-message notice: %q", m.Text)
			}
		})
	}
	// A known reason and known causes are said in the provider's words.
	m := secretSafe(statusErr(metav1.Status{Code: 422, Reason: metav1.StatusReasonInvalid,
		Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid}, {Type: metav1.CauseTypeFieldValueRequired}, {Type: "odd"}}}}))
	if m.Key != "kubernetes.edit.hiddenInvalidFields" || m.Params["count"] != "2" {
		t.Fatalf("got %+v", m)
	}
	if m := secretSafe(statusErr(metav1.Status{Code: 403, Reason: metav1.StatusReasonForbidden})); m.Key != "kubernetes.edit.hiddenForbidden" {
		t.Fatalf("got %+v", m)
	}
}

func paramValues(m core.Message) []string {
	var out []string
	for _, v := range m.Params {
		out = append(out, v)
	}
	return out
}

func TestOnlyALocalFailureIsACredentialFailure(t *testing.T) {
	if class, _ := classify(errors.New("getting credentials: exec: exit status 1")); class != provider.ClassUnauthorized {
		t.Fatalf("a credential plugin failure: %s", class)
	}
	if class, _ := classify(apierrors.NewInternalError(errors.New("getting credentials: vault down"))); class == provider.ClassUnauthorized {
		t.Fatal("a server's answer is not a local credential failure")
	}
}

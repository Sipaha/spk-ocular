package kubernetes

import (
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// knownCauses are the CauseType values the API server defines; a webhook
// may put any string there.
var knownCauses = map[metav1.CauseType]bool{
	metav1.CauseTypeFieldValueNotFound: true, metav1.CauseTypeFieldValueRequired: true,
	metav1.CauseTypeFieldValueDuplicate: true, metav1.CauseTypeFieldValueInvalid: true,
	metav1.CauseTypeFieldValueNotSupported: true, metav1.CauseTypeForbidden: true,
	metav1.CauseTypeTooLong: true, metav1.CauseTypeTooMany: true, metav1.CauseTypeInternal: true,
	metav1.CauseTypeTypeInvalid: true, metav1.CauseTypeUnexpectedServerResponse: true,
	metav1.CauseTypeFieldManagerConflict: true, metav1.CauseTypeResourceVersionTooLarge: true,
}

// secretSafe says why the server refused an edit of a Secret without any
// of its strings: a message, a reason or a cause field may carry a value
// (a webhook's text is not checked by the API server, and the masked
// original cannot tell what to strip). Only known enum values count, in
// the provider's own words.
func secretSafe(err error) core.Message {
	var se apierrors.APIStatus
	if !errors.As(err, &se) {
		return msg("edit.hiddenOther")
	}
	st := se.Status()
	switch st.Reason {
	case metav1.StatusReasonInvalid, metav1.StatusReasonBadRequest, metav1.StatusReasonRequestEntityTooLarge, metav1.StatusReasonUnsupportedMediaType:
		n := 0
		if st.Details != nil {
			for _, c := range st.Details.Causes {
				if knownCauses[c.Type] {
					n++
				}
			}
		}
		if n > 0 {
			return msg("edit.hiddenInvalidFields", "count", n)
		}
		return msg("edit.hiddenInvalid")
	case metav1.StatusReasonForbidden, metav1.StatusReasonUnauthorized:
		return msg("edit.hiddenForbidden")
	case metav1.StatusReasonConflict, metav1.StatusReasonAlreadyExists:
		return msg("edit.hiddenConflict")
	case metav1.StatusReasonNotFound, metav1.StatusReasonGone:
		return msg("edit.hiddenGone")
	}
	return msg("edit.hiddenOther")
}

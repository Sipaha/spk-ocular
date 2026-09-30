package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// maxValueBytes bounds one value (decoded bytes); maxValueInput bounds
// what is typed for it (1 MiB in base64 is ~1.4 MB, with room for lines).
const (
	maxValueBytes = 1 << 20
	maxValueInput = 3 << 19
)

// maxValueKey bounds a key's name (a Kubernetes Secret's is ≤ 253).
const maxValueKey = 253

// How a typed value is encoded.
const (
	// ValueText: the bytes are the text as typed (UTF-8, no normalising).
	ValueText = "text"
	// ValueBase64: standard base64 (line breaks are ignored).
	ValueBase64 = "base64"
)

// Signed value kinds (never taken for a text edit's "base"/"grant").
const (
	valueBaseKind  = "vbase"
	valueGrantKind = "vgrant"
)

// ValueRevealRequest is one key of one object (Ref.UID required).
type ValueRevealRequest struct {
	Ref core.Ref `json:"ref"`
	Key string   `json:"key"`
}

// ValueEditRequest is a key's change to review: set it to Value (in
// Encoding; empty is a value) or delete it, on the signed base of GetValues.
type ValueEditRequest struct {
	Ref      core.Ref `json:"ref"`
	Base     string   `json:"base"`
	Key      string   `json:"key"`
	Op       string   `json:"op"`
	Value    string   `json:"value,omitempty"`
	Encoding string   `json:"encoding,omitempty"`
}

// ValueRunRequest is a reviewed change: the same request and the plan's token.
type ValueRunRequest struct {
	ValueEditRequest
	Token string `json:"token"`
}

func valueHolderOf(sess provider.Session, ref core.Ref) (provider.ValueHolder, error) {
	vh, ok := sess.(provider.ValueHolder)
	if !ok {
		return nil, coded(CodeUnsupported, errors.New("objects of this target keep no values"))
	}
	for _, k := range sess.Kinds() {
		if k.ID == ref.Kind && k.Values {
			return vh, nil
		}
	}
	return nil, coded(CodeUnsupported, fmt.Errorf("%s keep no values", ref.Kind))
}

// decodeValue is the request's operation checked and its value's bytes
// (nil for a delete); a refusal never quotes the value.
func decodeValue(req ValueEditRequest) ([]byte, error) {
	if req.Key == "" || len(req.Key) > maxValueKey {
		return nil, coded(CodeBadRequest, fmt.Errorf("a key is 1 to %d characters", maxValueKey))
	}
	switch req.Op {
	case core.ValueDelete:
		if req.Value != "" {
			return nil, coded(CodeBadRequest, errors.New("deleting a key takes no value"))
		}
		return nil, nil
	case core.ValueSet:
	default:
		return nil, coded(CodeBadRequest, errors.New("unknown operation: set or delete"))
	}
	if len(req.Value) > maxValueInput {
		return nil, coded(CodeBadRequest, errors.New("the value is over 1 MiB"))
	}
	var b []byte
	switch req.Encoding {
	case ValueText:
		b = []byte(req.Value)
	case ValueBase64:
		// Strict: no bits past the value (one text, one set of bytes);
		// \r and \n are skipped by the decoder.
		var err error
		if b, err = base64.StdEncoding.Strict().DecodeString(req.Value); err != nil {
			return nil, coded(CodeBadRequest, errors.New("the value is not valid base64"))
		}
	default:
		return nil, coded(CodeBadRequest, errors.New("unknown encoding: text or base64"))
	}
	if len(b) > maxValueBytes {
		return nil, coded(CodeBadRequest, errors.New("the value is over 1 MiB"))
	}
	return b, nil
}

// GetValues lists an object's keys with sizes (no values), with a signed
// base for a key's change.
func (s *Service) GetValues(ctx context.Context, ref core.Ref) (core.ValueList, error) {
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.ValueList{}, err
	}
	vh, err := valueHolderOf(e.sess, ref)
	if err != nil {
		return core.ValueList{}, err
	}
	list, base, err := vh.Values(ctx, ref)
	if err != nil {
		return core.ValueList{}, fromProvider(err)
	}
	list.Ref.Provider, list.Ref.Target = ref.Provider, ref.Target
	if list.Base, err = s.signEdit(valueBaseKind, ref.Provider, ref.Target, s.configRev(e.hash), base); err != nil {
		return core.ValueList{}, coded(CodeInternal, err)
	}
	return list, nil
}

// RevealValue reads one key's value of that object (its UID) now: the only
// answer a value leaves in.
func (s *Service) RevealValue(ctx context.Context, req ValueRevealRequest) (core.Value, error) {
	if req.Ref.UID == "" {
		return core.Value{}, coded(CodeBadRequest, errors.New("which object: its UID is missing"))
	}
	if req.Key == "" || len(req.Key) > maxValueKey {
		return core.Value{}, coded(CodeBadRequest, fmt.Errorf("a key is 1 to %d characters", maxValueKey))
	}
	sess, err := s.session(ctx, req.Ref.Provider, req.Ref.Target)
	if err != nil {
		return core.Value{}, err
	}
	vh, err := valueHolderOf(sess, req.Ref)
	if err != nil {
		return core.Value{}, err
	}
	v, err := vh.RevealValue(ctx, req.Ref, req.Key)
	if err != nil {
		return core.Value{}, fromProvider(err)
	}
	return v, nil
}

// openValueEdit checks a value edit request: its value and its signed
// base; grantRev, when set, is the revision the plan's token was made in
// (another is another change). Then the session of the configuration the
// base was made in.
func (s *Service) openValueEdit(ctx context.Context, req ValueEditRequest, grantRev string) (provider.ValueEditRequest, string, provider.ValueHolder, error) {
	value, err := decodeValue(req)
	if err != nil {
		return provider.ValueEditRequest{}, "", nil, err
	}
	var base provider.ValueBase
	rev, err := s.openEdit(req.Base, valueBaseKind, req.Ref, &base)
	if err != nil {
		return provider.ValueEditRequest{}, "", nil, err
	}
	if grantRev != "" && grantRev != rev {
		return provider.ValueEditRequest{}, "", nil, coded(CodeBadRequest, errors.New("the plan is of another change"))
	}
	sess, err := s.checkedSession(ctx, req.Ref.Provider, req.Ref.Target, rev)
	if err != nil {
		return provider.ValueEditRequest{}, "", nil, err
	}
	vh, err := valueHolderOf(sess, req.Ref)
	if err != nil {
		return provider.ValueEditRequest{}, "", nil, err
	}
	return provider.ValueEditRequest{Ref: req.Ref, Base: base, Key: req.Key, Op: req.Op, Value: value}, rev, vh, nil
}

// PrepareValueEdit reviews a key's change (nothing changes, no value in
// the answer); a plan that can be written carries a signed token.
func (s *Service) PrepareValueEdit(ctx context.Context, req ValueEditRequest) (core.ValuePlan, error) {
	preq, rev, vh, err := s.openValueEdit(ctx, req, "")
	if err != nil {
		return core.ValuePlan{}, err
	}
	plan, grant, err := vh.PrepareValueEdit(ctx, preq)
	if err != nil {
		return core.ValuePlan{}, fromProvider(err)
	}
	plan.Where.Provider, plan.Where.Target = req.Ref.Provider, req.Ref.Target
	plan.Where = s.live(plan.Where)
	plan.Token = ""
	if grant != nil {
		if plan.Token, err = s.signEdit(valueGrantKind, req.Ref.Provider, req.Ref.Target, rev, grant); err != nil {
			return core.ValuePlan{}, coded(CodeInternal, err)
		}
	}
	return plan, nil
}

// RunValueEdit writes a reviewed change (its token) once, on the session
// of the configuration it was reviewed in.
func (s *Service) RunValueEdit(ctx context.Context, req ValueRunRequest) (core.ValueResult, error) {
	if req.Token == "" {
		return core.ValueResult{}, coded(CodeBadRequest, errors.New("no reviewed plan: review the change first"))
	}
	var grant provider.ValueGrant
	grev, err := s.openEdit(req.Token, valueGrantKind, req.Ref, &grant)
	if err != nil {
		return core.ValueResult{}, err
	}
	run, _, vh, err := s.openValueEdit(ctx, req.ValueEditRequest, grev)
	if err != nil {
		return core.ValueResult{}, err
	}
	res, err := vh.RunValueEdit(ctx, provider.ValueEditRun{ValueEditRequest: run, Grant: grant})
	if err != nil {
		return core.ValueResult{}, fromProvider(err)
	}
	return res, nil
}

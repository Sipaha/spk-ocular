package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// maxEditText bounds each text of an edit (an object in etcd is ≤ 1.5 MB).
const maxEditText = 3 << 20

// EditPrepareRequest is an edit to review: the text EditSource gave (with
// its signed base) and the edited one.
type EditPrepareRequest struct {
	Ref      core.Ref `json:"ref"`
	Base     string   `json:"base"`
	Original string   `json:"original"`
	Edited   string   `json:"edited"`
}

// EditRunRequest is a reviewed edit: the same texts and the plan's token.
type EditRunRequest struct {
	Ref      core.Ref `json:"ref"`
	Base     string   `json:"base"`
	Original string   `json:"original"`
	Edited   string   `json:"edited"`
	Token    string   `json:"token"`
}

// editEnvelope is what a signed edit value carries: its kind ("base" or
// "grant"), the target and configuration revision it was made in, and the
// provider's fields.
type editEnvelope struct {
	Kind     string          `json:"k"`
	Provider string          `json:"p"`
	Target   string          `json:"t"`
	Rev      string          `json:"r"`
	Payload  json.RawMessage `json:"v"`
}

// signEdit makes a value the UI carries but cannot make: the payload with
// an HMAC under the process key (a new process: old values mean nothing).
func (s *Service) signEdit(kind, providerID, target, rev string, payload any) (string, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(editEnvelope{Kind: kind, Provider: providerID, Target: target, Rev: rev, Payload: p})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(body)
	return enc + "." + s.editMAC(enc), nil
}

func (s *Service) editMAC(enc string) string {
	m := hmac.New(sha256.New, s.revKey)
	m.Write([]byte("edit|" + enc))
	return hex.EncodeToString(m.Sum(nil))
}

// openEdit checks a signed value of kind for ref's target and decodes its
// payload into out; it returns the revision it was made in.
func (s *Service) openEdit(v, kind string, ref core.Ref, out any) (string, error) {
	enc, mac, ok := strings.Cut(v, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(s.editMAC(enc))) {
		return "", coded(CodeBadRequest, fmt.Errorf("the %s is not one this application made: open the editor again", kind))
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", coded(CodeBadRequest, err)
	}
	var e editEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		return "", coded(CodeBadRequest, err)
	}
	if e.Kind != kind || e.Provider != ref.Provider || e.Target != ref.Target || e.Rev == "" {
		return "", coded(CodeBadRequest, fmt.Errorf("the %s is of another edit", kind))
	}
	if err := json.Unmarshal(e.Payload, out); err != nil {
		return "", coded(CodeBadRequest, err)
	}
	return e.Rev, nil
}

func editorOf(sess provider.Session, ref core.Ref) (provider.Editor, error) {
	ed, ok := sess.(provider.Editor)
	if !ok {
		return nil, coded(CodeUnsupported, errors.New("objects of this target cannot be edited"))
	}
	for _, k := range sess.Kinds() {
		if k.ID == ref.Kind && k.Editable {
			return ed, nil
		}
	}
	return nil, coded(CodeUnsupported, fmt.Errorf("%s cannot be edited", ref.Kind))
}

func checkEditTexts(original, edited string) error {
	if len(original) > maxEditText || len(edited) > maxEditText {
		return coded(CodeBadRequest, fmt.Errorf("the text is over %d MB", maxEditText>>20))
	}
	return nil
}

// GetEditSource reads an object's text for the editor, with a signed base.
func (s *Service) GetEditSource(ctx context.Context, ref core.Ref) (core.EditDoc, error) {
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.EditDoc{}, err
	}
	ed, err := editorOf(e.sess, ref)
	if err != nil {
		return core.EditDoc{}, err
	}
	doc, base, err := ed.EditSource(ctx, ref)
	if err != nil {
		return core.EditDoc{}, fromProvider(err)
	}
	doc.Ref.Provider, doc.Ref.Target = ref.Provider, ref.Target
	if doc.Base, err = s.signEdit("base", ref.Provider, ref.Target, s.configRev(e.hash), base); err != nil {
		return core.EditDoc{}, coded(CodeInternal, err)
	}
	return doc, nil
}

// PrepareEdit reviews an edit (nothing changes); a plan that can be
// written carries a signed token for RunEdit.
func (s *Service) PrepareEdit(ctx context.Context, req EditPrepareRequest) (core.EditPlan, error) {
	if err := checkEditTexts(req.Original, req.Edited); err != nil {
		return core.EditPlan{}, err
	}
	var base provider.EditBase
	rev, err := s.openEdit(req.Base, "base", req.Ref, &base)
	if err != nil {
		return core.EditPlan{}, err
	}
	sess, err := s.checkedSession(ctx, req.Ref.Provider, req.Ref.Target, rev)
	if err != nil {
		return core.EditPlan{}, err
	}
	ed, err := editorOf(sess, req.Ref)
	if err != nil {
		return core.EditPlan{}, err
	}
	plan, grant, err := ed.PrepareEdit(ctx, provider.EditRequest{Ref: req.Ref, Base: base, Original: req.Original, Edited: req.Edited})
	if err != nil {
		return core.EditPlan{}, fromProvider(err)
	}
	plan.Where.Provider, plan.Where.Target = req.Ref.Provider, req.Ref.Target
	plan.Where = s.live(plan.Where)
	plan.Token = ""
	if grant != nil {
		if plan.Token, err = s.signEdit("grant", req.Ref.Provider, req.Ref.Target, rev, grant); err != nil {
			return core.EditPlan{}, coded(CodeInternal, err)
		}
	}
	return plan, nil
}

// RunEdit writes a reviewed edit (its token) once, on the session of the
// configuration it was reviewed in.
func (s *Service) RunEdit(ctx context.Context, req EditRunRequest) (core.EditResult, error) {
	if err := checkEditTexts(req.Original, req.Edited); err != nil {
		return core.EditResult{}, err
	}
	if req.Token == "" {
		return core.EditResult{}, coded(CodeBadRequest, errors.New("no reviewed plan: review the edit first"))
	}
	var base provider.EditBase
	rev, err := s.openEdit(req.Base, "base", req.Ref, &base)
	if err != nil {
		return core.EditResult{}, err
	}
	var grant provider.EditGrant
	grev, err := s.openEdit(req.Token, "grant", req.Ref, &grant)
	if err != nil {
		return core.EditResult{}, err
	}
	if grev != rev {
		return core.EditResult{}, coded(CodeBadRequest, errors.New("the plan is of another edit"))
	}
	sess, err := s.checkedSession(ctx, req.Ref.Provider, req.Ref.Target, rev)
	if err != nil {
		return core.EditResult{}, err
	}
	ed, err := editorOf(sess, req.Ref)
	if err != nil {
		return core.EditResult{}, err
	}
	res, err := ed.RunEdit(ctx, provider.EditRun{
		EditRequest: provider.EditRequest{Ref: req.Ref, Base: base, Original: req.Original, Edited: req.Edited},
		Grant:       grant,
	})
	if err != nil {
		return core.EditResult{}, fromProvider(err)
	}
	return res, nil
}

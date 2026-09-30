package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var secretRef = core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "secrets", Name: "db"}

// valueMarker is a value that must leave the API only in RevealValue's
// answer.
const valueMarker = "MARKER-7f3c-secret-value"

// valueSession keeps secrets' values and records what the API hands it.
type valueSession struct {
	*fakeSession
	o *valueOpenable
}

func (v *valueSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{
		{ID: "secrets", Title: "Secrets", Scoped: true, Values: true},
		{ID: "configmaps", Title: "ConfigMaps", Scoped: true},
	}
}

var valueBase = provider.ValueBase{Route: "r", Namespace: "ns", Name: "db", UID: "uid-db", Version: "7",
	Keys: []provider.ValuePrint{{Key: "password", Present: true, Print: "p1"}}}

var valueGrant = provider.ValueGrant{Route: "r", Namespace: "ns", Name: "db", UID: "uid-db", Version: "8", Digest: "d",
	Expected: []provider.ValuePrint{{Key: "password", Present: true, Print: "p2"}}, Mode: "checked"}

func (v *valueSession) Values(_ context.Context, ref core.Ref) (core.ValueList, provider.ValueBase, error) {
	ref.UID = "uid-db"
	return core.ValueList{Ref: ref, Version: "7", Keys: []core.ValueKey{{Key: "password", Size: len(valueMarker), Text: true}}}, valueBase, nil
}

func (v *valueSession) RevealValue(_ context.Context, ref core.Ref, key string) (core.Value, error) {
	v.o.mu.Lock()
	v.o.revealed = append(v.o.revealed, ref)
	v.o.mu.Unlock()
	return core.Value{Key: key, Size: len(valueMarker), Text: true, Value: valueMarker, UID: ref.UID, Version: "7"}, nil
}

func (v *valueSession) PrepareValueEdit(_ context.Context, req provider.ValueEditRequest) (core.ValuePlan, *provider.ValueGrant, error) {
	v.o.mu.Lock()
	v.o.prepared = append(v.o.prepared, req)
	noGrant := v.o.noGrant
	v.o.mu.Unlock()
	plan := core.ValuePlan{Where: core.LiveTarget{Provider: "k", Target: v.target, ConfigHash: v.hash, Ref: req.Ref},
		Key: req.Key, Op: req.Op, Before: len(valueMarker), After: len(req.Value), Changed: true}
	if noGrant {
		return plan, nil, nil
	}
	g := valueGrant
	return plan, &g, nil
}

func (v *valueSession) RunValueEdit(_ context.Context, run provider.ValueEditRun) (core.ValueResult, error) {
	v.o.mu.Lock()
	defer v.o.mu.Unlock()
	v.o.runs = append(v.o.runs, run)
	return core.ValueResult{Message: "written", Version: "9"}, nil
}

type valueOpenable struct {
	*openable
	mu       sync.Mutex
	revealed []core.Ref
	prepared []provider.ValueEditRequest
	runs     []provider.ValueEditRun
	noGrant  bool
}

func (o *valueOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	return &valueSession{fakeSession: s.(*fakeSession), o: o}, nil
}

func newValueService(t *testing.T) (*Service, *valueOpenable) {
	t.Helper()
	k := &valueOpenable{openable: newOpenable("a", "b")}
	k.setHash("a", "ha")
	s, _ := newService(t, k)
	return s, k
}

// setReq is a text value's change of password on list's base.
func setReq(list core.ValueList, value string) ValueEditRequest {
	return ValueEditRequest{Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: value, Encoding: ValueText}
}

func TestValueTokensAreSignedAndBindTheReview(t *testing.T) {
	s, k := newValueService(t)
	ctx := context.Background()
	list, err := s.GetValues(ctx, secretRef)
	require.NoError(t, err)
	require.NotEmpty(t, list.Base)
	assert.Equal(t, "k", list.Ref.Provider)
	assert.Equal(t, "a", list.Ref.Target)
	assert.Equal(t, "uid-db", list.Ref.UID)

	prep := setReq(list, "new")
	plan, err := s.PrepareValueEdit(ctx, prep)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Token)
	assert.NotEmpty(t, plan.Where.ConfigRev)
	require.Len(t, k.prepared, 1)
	assert.Equal(t, valueBase, k.prepared[0].Base)
	assert.Equal(t, []byte("new"), k.prepared[0].Value)
	assert.Equal(t, "password", k.prepared[0].Key)
	assert.Equal(t, core.ValueSet, k.prepared[0].Op)

	// A forged base, or a text edit's base, is refused before the provider sees it.
	doc := setReq(list, "new")
	doc.Base = flip(list.Base)
	_, err = s.PrepareValueEdit(ctx, doc)
	assertCode(t, err, CodeBadRequest)
	textBase, err := s.signEdit("base", "k", "a", s.configRev("ha"), provider.EditBase{Route: "r"})
	require.NoError(t, err)
	doc.Base = textBase
	_, err = s.PrepareValueEdit(ctx, doc)
	assertCode(t, err, CodeBadRequest)
	assert.Len(t, k.prepared, 1)

	run := ValueRunRequest{ValueEditRequest: prep, Token: plan.Token}
	otherTarget := run
	otherTarget.Ref.Target = "b"
	textGrant, err := s.signEdit("grant", "k", "a", s.configRev("ha"), provider.EditGrant{Route: "r"})
	require.NoError(t, err)
	for name, bad := range map[string]ValueRunRequest{
		"no token":             {ValueEditRequest: prep},
		"forged token":         {ValueEditRequest: prep, Token: flip(plan.Token)},
		"the base as a token":  {ValueEditRequest: prep, Token: list.Base},
		"a text edit's grant":  {ValueEditRequest: prep, Token: textGrant},
		"another target":       otherTarget,
		"another object's key": {ValueEditRequest: ValueEditRequest{Ref: core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "configmaps", Name: "db"}, Base: list.Base, Key: "password", Op: core.ValueSet, Value: "new", Encoding: ValueText}, Token: plan.Token},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.RunValueEdit(ctx, bad)
			require.Error(t, err)
			assert.Empty(t, k.runs)
		})
	}
	// Another process's key: its tokens mean nothing here.
	other, _ := newValueService(t)
	_, err = other.RunValueEdit(ctx, run)
	assertCode(t, err, CodeBadRequest)

	res, err := s.RunValueEdit(ctx, run)
	require.NoError(t, err)
	assert.Equal(t, "written", res.Message)
	require.Len(t, k.runs, 1)
	assert.Equal(t, valueGrant, k.runs[0].Grant)
	assert.Equal(t, valueBase, k.runs[0].Base)
	assert.Equal(t, []byte("new"), k.runs[0].Value)
}

func TestValueEditWithoutAGrantHasNoToken(t *testing.T) {
	s, k := newValueService(t)
	k.noGrant = true
	list, err := s.GetValues(context.Background(), secretRef)
	require.NoError(t, err)
	plan, err := s.PrepareValueEdit(context.Background(), setReq(list, "x"))
	require.NoError(t, err)
	assert.Empty(t, plan.Token)
}

func TestValueEditAfterAReconfigurationIsAConflict(t *testing.T) {
	s, k := newValueService(t)
	ctx := context.Background()
	list, err := s.GetValues(ctx, secretRef)
	require.NoError(t, err)
	plan, err := s.PrepareValueEdit(ctx, setReq(list, "x"))
	require.NoError(t, err)
	k.setHash("a", "ha2")
	_, err = s.RunValueEdit(ctx, ValueRunRequest{ValueEditRequest: setReq(list, "x"), Token: plan.Token})
	assertCode(t, err, CodeConflict)
	_, err = s.PrepareValueEdit(ctx, setReq(list, "x"))
	assertCode(t, err, CodeConflict)

	// A grant of the new configuration does not go with a base of the old.
	list2, err := s.GetValues(ctx, secretRef)
	require.NoError(t, err)
	plan2, err := s.PrepareValueEdit(ctx, setReq(list2, "x"))
	require.NoError(t, err)
	_, err = s.RunValueEdit(ctx, ValueRunRequest{ValueEditRequest: setReq(list, "x"), Token: plan2.Token})
	assertCode(t, err, CodeBadRequest)
	assert.Empty(t, k.runs)
}

func TestValueEditDecodesTheValueExactly(t *testing.T) {
	s, k := newValueService(t)
	ctx := context.Background()
	list, err := s.GetValues(ctx, secretRef)
	require.NoError(t, err)
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	for name, c := range map[string]struct {
		value, encoding string
		want            []byte
	}{
		"text is taken as typed":         {"a\r\nb\n", ValueText, []byte("a\r\nb\n")},
		"empty text is a value":          {"", ValueText, []byte{}},
		"base64 bytes":                   {b64([]byte{0, 0xff, '\r', 'x'}), ValueBase64, []byte{0, 0xff, '\r', 'x'}},
		"base64 wrapped in lines":        {"YWJj\nZGVm\r\n", ValueBase64, []byte("abcdef")},
		"empty base64 is an empty value": {"", ValueBase64, []byte{}},
	} {
		t.Run(name, func(t *testing.T) {
			req := setReq(list, c.value)
			req.Encoding = c.encoding
			_, err := s.PrepareValueEdit(ctx, req)
			require.NoError(t, err)
			got := k.prepared[len(k.prepared)-1].Value
			require.NotNil(t, got, "an empty value is not no value")
			assert.Equal(t, c.want, got)
		})
	}

	del := ValueEditRequest{Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueDelete}
	_, err = s.PrepareValueEdit(ctx, del)
	require.NoError(t, err)
	assert.Nil(t, k.prepared[len(k.prepared)-1].Value)

	n := len(k.prepared)
	for name, bad := range map[string]ValueEditRequest{
		"bad base64":            {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: "YWJj!", Encoding: ValueBase64},
		"base64 with bad bits":  {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: "YWJ=", Encoding: ValueBase64},
		"no encoding":           {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: "x"},
		"unknown encoding":      {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: "x", Encoding: "hex"},
		"a delete with a value": {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueDelete, Value: "x", Encoding: ValueText},
		"unknown operation":     {Ref: list.Ref, Base: list.Base, Key: "password", Op: "append", Value: "x", Encoding: ValueText},
		"text over 1 MiB":       {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: strings.Repeat("x", maxValueBytes+1), Encoding: ValueText},
		"base64 of over 1 MiB":  {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: b64(make([]byte, maxValueBytes+1)), Encoding: ValueBase64},
		"base64 input too long": {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: strings.Repeat("\n", maxValueInput+1), Encoding: ValueBase64},
		"a key over 253":        {Ref: list.Ref, Base: list.Base, Key: strings.Repeat("k", 254), Op: core.ValueSet, Value: "x", Encoding: ValueText},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.PrepareValueEdit(ctx, bad)
			assertCode(t, err, CodeBadRequest)
			_, err = s.RunValueEdit(ctx, ValueRunRequest{ValueEditRequest: bad, Token: "x.y"})
			assertCode(t, err, CodeBadRequest)
		})
	}
	assert.Len(t, k.prepared, n, "a bad value never reaches the provider")
	assert.Empty(t, k.runs)

	// A value of exactly 1 MiB is taken, in either encoding.
	for _, req := range []ValueEditRequest{
		{Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: strings.Repeat("x", maxValueBytes), Encoding: ValueText},
		{Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: b64(make([]byte, maxValueBytes)), Encoding: ValueBase64},
		{Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: wrapped(b64(make([]byte, maxValueBytes))), Encoding: ValueBase64},
	} {
		_, err := s.PrepareValueEdit(ctx, req)
		require.NoError(t, err)
		assert.Len(t, k.prepared[len(k.prepared)-1].Value, maxValueBytes)
	}
}

func TestRevealValue(t *testing.T) {
	s, k := newValueService(t)
	ctx := context.Background()
	ref := secretRef
	ref.UID = "uid-db"
	v, err := s.RevealValue(ctx, ValueRevealRequest{Ref: ref, Key: "password"})
	require.NoError(t, err)
	assert.Equal(t, valueMarker, v.Value)
	require.Len(t, k.revealed, 1)
	assert.Equal(t, "uid-db", k.revealed[0].UID)

	// Which object: without a UID nothing is read.
	_, err = s.RevealValue(ctx, ValueRevealRequest{Ref: secretRef, Key: "password"})
	assertCode(t, err, CodeBadRequest)
	_, err = s.RevealValue(ctx, ValueRevealRequest{Ref: ref, Key: ""})
	assertCode(t, err, CodeBadRequest)
	assert.Len(t, k.revealed, 1)
}

func TestValuesOfKindsWithoutValuesAreUnsupported(t *testing.T) {
	s, _ := newValueService(t)
	ctx := context.Background()
	cm := core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "configmaps", Name: "db", UID: "u"}
	_, err := s.GetValues(ctx, cm)
	assertCode(t, err, CodeUnsupported)
	_, err = s.RevealValue(ctx, ValueRevealRequest{Ref: cm, Key: "k"})
	assertCode(t, err, CodeUnsupported)

	plain, _ := newActionService(t) // sessions that are not ValueHolders
	_, err = plain.GetValues(ctx, core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "secrets", Name: "db"})
	assertCode(t, err, CodeUnsupported)
}

// The value typed or read leaves only in RevealValue's answer: not in a
// base, token, plan, result or error, decoded or in base64.
func TestTheValueLeavesOnlyInReveal(t *testing.T) {
	s, _ := newValueService(t)
	ctx := context.Background()
	b64 := base64.StdEncoding.EncodeToString([]byte(valueMarker))
	var seen []string
	see := func(v any, err error) {
		if err != nil {
			seen = append(seen, err.Error())
		}
		b, _ := json.Marshal(v)
		seen = append(seen, string(b))
	}
	list, err := s.GetValues(ctx, secretRef)
	see(list, err)
	for _, req := range []ValueEditRequest{setReq(list, valueMarker), {Ref: list.Ref, Base: list.Base, Key: "password", Op: core.ValueSet, Value: b64, Encoding: ValueBase64}} {
		plan, err := s.PrepareValueEdit(ctx, req)
		see(plan, err)
		res, err := s.RunValueEdit(ctx, ValueRunRequest{ValueEditRequest: req, Token: plan.Token})
		see(res, err)
	}
	// Refusals of a bad value do not quote it.
	bad := setReq(list, valueMarker+"!")
	bad.Encoding = ValueBase64
	see(s.PrepareValueEdit(ctx, bad))
	bad = setReq(list, valueMarker)
	bad.Op = core.ValueDelete
	see(s.PrepareValueEdit(ctx, bad))
	bad = setReq(list, valueMarker)
	bad.Encoding = valueMarker
	see(s.PrepareValueEdit(ctx, bad))

	for _, out := range seen {
		for _, part := range tokenParts(out) {
			assert.NotContains(t, part, valueMarker)
			assert.NotContains(t, part, b64)
		}
	}
}

// tokenParts is s and the decoded payloads of the signed values in it.
func tokenParts(s string) []string {
	out := []string{s}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '"' || r == '.' }) {
		if b, err := base64.RawURLEncoding.DecodeString(f); err == nil {
			out = append(out, string(b))
		}
	}
	return out
}

// wrapped is s in lines of 76 ending with CRLF (as mail and some tools write base64).
func wrapped(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 76 {
		b.WriteString(s[i:min(i+76, len(s))])
		b.WriteString("\r\n")
	}
	return b.String()
}

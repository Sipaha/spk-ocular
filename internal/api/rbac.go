package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type RBACRequest struct {
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
	Subject  *core.Ref     `json:"subject,omitempty"`
}
type AccessRequest struct {
	Provider   string                `json:"provider"`
	Target     string                `json:"target"`
	Ref        *core.Ref             `json:"ref,omitempty"`
	Attributes core.AccessAttributes `json:"attributes"`
}

func rbacKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.rbac", Title: "RBAC explanation", Group: "Access Control", Workspace: "rbac", Scoped: true, Columns: []core.Column{}}
}
func (s *Service) RBACSnapshot(ctx context.Context, req RBACRequest) (core.RBACSnapshot, error) {
	if !fromUI(ctx) {
		return core.RBACSnapshot{}, coded("forbidden", errors.New("RBAC explanation is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.RBACSnapshot{}, rbacInputError("invalid namespace selection")
	}
	if req.Subject != nil && (req.Subject.Provider != req.Provider || req.Subject.Target != req.Target || req.Subject.Name == "" || req.Subject.Scope == "" || !req.Scope.Contains(req.Subject.Scope)) {
		return core.RBACSnapshot{}, rbacInputError("ServiceAccount must belong to this target and namespace selection")
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.RBACSnapshot{}, err
	}
	source, ok := e.sess.(provider.RBACSource)
	if !ok {
		return core.RBACSnapshot{}, coded(CodeUnsupported, errors.New("connection does not support RBAC explanation"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return source.RBAC(ctx, req.Scope, req.Subject)
}
func (s *Service) CheckAccess(ctx context.Context, req AccessRequest) (core.AccessReview, error) {
	if !fromUI(ctx) {
		return core.AccessReview{}, coded("forbidden", errors.New("access checks are available only to the application UI"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.AccessReview{}, err
	}
	source, ok := e.sess.(provider.RBACSource)
	if !ok {
		return core.AccessReview{}, coded(CodeUnsupported, errors.New("connection does not support access checks"))
	}
	a := req.Attributes
	if req.Ref != nil {
		if req.Ref.Provider != req.Provider || req.Ref.Target != req.Target {
			return core.AccessReview{}, rbacInputError("resource belongs to another target")
		}
		a, err = source.ResolveAccess(*req.Ref, a.Verb, a.Subresource)
		if err != nil {
			return core.AccessReview{}, err
		}
	}
	if a.Verb == "" || a.Resource == "" || strings.ContainsAny(a.Resource, "/\\ \t\r\n") || strings.ContainsAny(a.Subresource, "/\\ \t\r\n") {
		return core.AccessReview{}, rbacInputError("provide an API resource, verb and separate subresource")
	}
	if a.Name != "" && (a.Verb == "deletecollection" || (a.Verb == "create" && a.Subresource == "")) {
		return core.AccessReview{}, rbacInputError("name is not applicable to top-level create or deletecollection")
	}
	for _, v := range []string{a.Verb, a.Group, a.Resource, a.Subresource, a.Namespace, a.Name} {
		if len(v) > 253 || strings.ContainsAny(v, "\x00\r\n") {
			return core.AccessReview{}, rbacInputError("invalid access attributes")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return source.CheckAccess(ctx, a)
}

func rbacInputError(detail string) *CodedError {
	why := apiMessage("rbacInput")
	return &CodedError{Code: CodeBadRequest, Detail: detail, Why: &why}
}

package kubernetes

import (
	"context"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func (s *session) Resync(q provider.Query) error { return s.PrepareRefresh(s.ctx, q) }

func (s *session) PrepareRefresh(ctx context.Context, q provider.Query) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	def := s.kind(q.Kind)
	if def == nil {
		return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", q.Kind)}
	}
	if !q.Scope.Valid() {
		return &provider.Error{Class: provider.ClassInvalid, Message: "invalid scope selector"}
	}
	sources := []problemSource{{def: def}}
	if def == problemsKind {
		sources = s.resolvedProblemSources()
	}
	return s.caches.invalidate(func(key cacheKey) bool {
		for _, src := range sources {
			if key.gvr != src.def.gvr {
				continue
			}
			if def == problemsKind && q.Scope.Mode == core.ScopeNone && src.def.namespaced {
				continue
			}
			if src.def.namespaced && q.Scope.Mode == core.ScopeSome {
				found := false
				for _, ns := range q.Scope.SelectedNames() {
					if key.namespace == ns {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			} else {
				ns := ""
				if src.def.namespaced && q.Scope.Mode == core.ScopeOne {
					ns = q.Scope.Name
				}
				if q.Subject != nil {
					ns = q.Subject.Scope
				}
				if key.namespace != ns {
					continue
				}
			}
			selector := src.selector
			if q.Name != "" {
				selector = "metadata.name=" + q.Name
			}
			if q.Subject != nil {
				selector = "involvedObject.uid=" + q.Subject.UID
			}
			if key.selector != selector {
				continue
			}
			if key.sch != nil && key.sch.epoch != q.Schema {
				continue
			}
			return true
		}
		return false
	})
}

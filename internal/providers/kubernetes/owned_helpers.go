package kubernetes

import (
	"context"
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (s *session) CleanupOwnedHelper(ctx context.Context, r core.OwnedHelper, pin func(string) error) (bool, error) {
	if r.Provider != ProviderID || r.Target != s.conn.target || r.ID == "" || r.NamespaceUID == "" || r.NodeUID == "" {
		return false, errors.New("helper recovery identity is incomplete")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	ns, err := s.conn.dyn.Resource(namespaceGV).Get(ctx, r.Namespace, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	// A repointed connection must retain the record, not forget an object in
	// the original cluster or delete one in another namespace incarnation.
	if string(ns.GetUID()) != r.NamespaceUID {
		return false, errors.New("helper namespace identity changed")
	}
	p, err := s.conn.dyn.Resource(podsKind.gvr).Namespace(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if r.UID != "" && string(p.GetUID()) != r.UID {
		return true, nil
	}
	ownedNode := false
	for _, owner := range p.GetOwnerReferences() {
		if owner.APIVersion == "v1" && owner.Kind == "Node" && owner.Name == r.NodeName && string(owner.UID) == r.NodeUID {
			ownedNode = true
		}
	}
	h := &nodeShellHandle{conn: s.conn, g: nodeShellGrant{Namespace: r.Namespace, Name: r.Name, Nonce: r.ID, Ref: core.Ref{Name: r.NodeName}}}
	h.writer = s.writer
	if h.writer == nil {
		h.writer = dynWriter{s.dyn}
	}
	if !ownedNode || !h.owns(p) {
		return false, errors.New("helper ownership no longer matches; nothing deleted")
	}
	uid := string(p.GetUID())
	if r.UID == "" {
		if err = pin(uid); err != nil {
			return false, err
		}
	}
	err = h.cleanupWith(ctx, types.UID(uid))
	return err == nil, err
}

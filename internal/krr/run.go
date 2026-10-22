package krr

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// RunOptions contains explicit operator choices, never a credential source.
// Assertions disclose trust boundaries; they do not authenticate source identity
// or establish the ServiceAccount's effective authorization.
type RunOptions struct {
	Namespace       string `json:"namespace"`
	ServiceAccount  string `json:"serviceAccount"`
	Source          string `json:"source"`
	Label           string `json:"label"`
	Cluster         string `json:"cluster"`
	SingleCluster   bool   `json:"singleCluster"`
	ReviewedAccount bool   `json:"reviewedAccount"`
	NoSourceAuth    bool   `json:"noSourceAuth"`
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var metricLabel = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (o RunOptions) Valid() bool {
	if !o.ReviewedAccount || !o.NoSourceAuth || len(o.Namespace) > 63 || !dnsLabel.MatchString(o.Namespace) || len(o.ServiceAccount) > 253 || len(o.ServiceAccount) == 0 {
		return false
	}
	for _, part := range strings.Split(o.ServiceAccount, ".") {
		if len(part) > 63 || !dnsLabel.MatchString(part) {
			return false
		}
	}
	u, e := url.Parse(o.Source)
	if e != nil || len(o.Source) > 1024 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsFunc(o.Source, unicode.IsControl) {
		return false
	}
	if o.SingleCluster {
		return o.Label == "" && o.Cluster == ""
	}
	return len(o.Label) <= 128 && metricLabel.MatchString(o.Label) && len(o.Cluster) > 0 && len(o.Cluster) <= 128 && !strings.ContainsAny(o.Cluster, "\"\\\n\r") && !strings.ContainsFunc(o.Cluster, unicode.IsControl)
}

type Review struct {
	Options           RunOptions `json:"options"`
	Expect            string     `json:"expect"`
	ConfigRev         string     `json:"configRev"`
	Image             string     `json:"image"`
	NamespaceUID      string     `json:"namespaceUID"`
	ServiceAccountUID string     `json:"serviceAccountUID"`
	Name              string     `json:"name"`
	DeadlineSeconds   int        `json:"deadlineSeconds"`
}

// Execution is implemented by the connected provider, never a host process.
type Execution interface {
	Execute(context.Context) ([]byte, error)
}

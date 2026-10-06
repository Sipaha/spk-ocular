package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"helm.sh/helm/v4/pkg/action"
	chartutil "helm.sh/helm/v4/pkg/chart/common/util"
	chartv2util "helm.sh/helm/v4/pkg/chart/v2/util"
	releaseapi "helm.sh/helm/v4/pkg/release"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/util/validation"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func asRelease(raw releaseapi.Releaser) (*release.Release, error) {
	switch r := raw.(type) {
	case *release.Release:
		if r != nil {
			return r, nil
		}
	case release.Release:
		return &r, nil
	}
	return nil, errors.New("unsupported Helm release format")
}
func summary(r *release.Release) Release {
	out := Release{Name: r.Name, Namespace: r.Namespace, Revision: r.Version}
	if r.Info != nil {
		out.Status = string(r.Info.Status)
		out.Updated = r.Info.LastDeployed
	}
	if r.Chart != nil && r.Chart.Metadata != nil {
		out.Chart = r.Chart.Metadata.Name
		out.ChartVersion = r.Chart.Metadata.Version
		out.AppVersion = r.Chart.Metadata.AppVersion
	}
	return out
}
func List(a *action.Configuration) ([]Release, error) {
	l := action.NewList(a)
	l.All = true
	l.StateMask = action.ListAll
	l.Limit = 0
	raw, err := l.Run()
	if err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		rel, err := asRelease(r)
		if err != nil {
			return nil, err
		}
		out = append(out, summary(rel))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func Get(a *action.Configuration, name string, revision int) (Detail, error) {
	g := action.NewGet(a)
	g.Version = revision
	raw, err := g.Run(name)
	if err != nil {
		return Detail{}, err
	}
	r, err := asRelease(raw)
	if err != nil {
		return Detail{}, err
	}
	resources, err := manifestResources(r.Manifest)
	if err != nil {
		return Detail{}, err
	}
	out := Detail{Release: summary(r), Manifest: r.Manifest, History: []Release{}, Resources: resources}
	if r.Info != nil {
		out.Notes = r.Info.Notes
	}
	values, err := yaml.Marshal(r.Config)
	if err != nil {
		return out, err
	}
	out.Values = string(values)
	computed, err := chartutil.CoalesceValues(r.Chart, r.Config)
	if err != nil {
		return out, err
	}
	values, err = yaml.Marshal(computed)
	if err != nil {
		return out, err
	}
	out.ComputedValues = string(values)
	for _, h := range r.Hooks {
		out.Hooks += "---\n" + h.Manifest + "\n"
	}
	h := action.NewHistory(a)
	h.Max = 0
	history, err := h.Run(name)
	if err != nil {
		return out, err
	}
	for _, raw := range history {
		r, err := asRelease(raw)
		if err != nil {
			return out, err
		}
		out.History = append(out.History, summary(r))
	}
	sort.Slice(out.History, func(i, j int) bool { return out.History[i].Revision > out.History[j].Revision })
	return out, nil
}
func current(a *action.Configuration, name string) (*release.Release, string, error) {
	raw, err := a.Releases.Last(name)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, "absent", nil
	}
	if err != nil {
		return nil, "", err
	}
	r, err := asRelease(raw)
	if err != nil {
		return nil, "", err
	}
	fingerprint, err := releaseFingerprint(r)
	return r, fingerprint, err
}
func releaseFingerprint(r *release.Release) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

func checkOperation(o Operation) error {
	switch o.Action {
	case "install", "upgrade", "rollback", "uninstall":
	default:
		return errors.New("unknown Helm operation")
	}
	if len(validation.IsDNS1123Label(o.Namespace)) != 0 {
		return errors.New("invalid namespace name")
	}
	if err := chartv2util.ValidateReleaseName(o.Name); err != nil {
		return err
	}
	if o.Name == "" || o.Namespace == "" {
		return errors.New("release name and namespace are required")
	}
	if o.TimeoutSeconds < 1 || o.TimeoutSeconds > 1800 {
		return errors.New("timeout must be between 1 and 1800 seconds")
	}
	if len(o.Values) > 4<<20 {
		return errors.New("values exceed 4 MiB")
	}
	if o.Action == "rollback" && o.Revision < 1 {
		return errors.New("select a rollback revision")
	}
	return nil
}
func checkCurrent(o Operation, r *release.Release) error {
	if o.Action == "install" {
		if r != nil {
			return fmt.Errorf("release %s already exists", o.Name)
		}
		return nil
	}
	if r == nil {
		return driver.ErrReleaseNotFound
	}
	if r.Info != nil && strings.HasPrefix(string(r.Info.Status), "pending-") {
		return errors.New("another Helm operation is pending")
	}
	return nil
}

func manifestResources(manifest string) ([]Resource, error) {
	out := []Resource{}
	decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	for {
		var obj struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if obj.Kind != "" {
			out = append(out, Resource{APIVersion: obj.APIVersion, Kind: obj.Kind, Name: obj.Metadata.Name, Namespace: obj.Metadata.Namespace})
		}
	}
	return out, nil
}

package helm

import (
	"errors"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"sigs.k8s.io/yaml"
)

func hasCRDs(ch chart.Charter) bool {
	c, ok := ch.(*chartv2.Chart)
	return ok && len(c.CRDObjects()) > 0
}

// CRDs do not exist until installation, so a server dry-run cannot resolve their
// resources. Render locally using observed cluster capabilities plus the chart's
// declared CRDs, and label the review explicitly. Never silently downgrade an
// unrelated server validation error to a local preview.
func configureCRDPreview(i *action.Install, a *action.Configuration, ch chart.Charter) error {
	caps := a.Capabilities
	if a.RESTClientGetter != nil {
		discovery, err := a.RESTClientGetter.ToDiscoveryClient()
		if err != nil {
			return err
		}
		version, err := discovery.ServerVersion()
		if err != nil {
			return err
		}
		apis, err := action.GetVersionSet(discovery)
		if err != nil {
			return err
		}
		caps = &common.Capabilities{KubeVersion: common.KubeVersion{Version: version.GitVersion, Major: version.Major, Minor: version.Minor}, APIVersions: apis}
	}
	if caps == nil {
		return errors.New("cluster capabilities are required for a CRD preview")
	}
	i.KubeVersion = &caps.KubeVersion
	i.APIVersions = append([]string(nil), caps.APIVersions...)
	for _, crd := range ch.(*chartv2.Chart).CRDObjects() {
		var obj struct {
			Spec struct {
				Group    string `json:"group"`
				Version  string `json:"version"`
				Versions []struct {
					Name   string `json:"name"`
					Served bool   `json:"served"`
				} `json:"versions"`
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(crd.File.Data, &obj); err != nil {
			return err
		}
		versions := []string{}
		if obj.Spec.Version != "" {
			versions = append(versions, obj.Spec.Version)
		}
		for _, v := range obj.Spec.Versions {
			if v.Served {
				versions = append(versions, v.Name)
			}
		}
		for _, v := range versions {
			gv := obj.Spec.Group + "/" + v
			i.APIVersions = append(i.APIVersions, gv, gv+"/"+obj.Spec.Names.Kind)
		}
	}
	i.DryRunStrategy = action.DryRunClient
	i.IncludeCRDs = true
	return nil
}

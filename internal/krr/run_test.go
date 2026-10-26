package krr

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAutomaticRunOptionsNeedNoAccountAssertionsButKeepInputBounds(t *testing.T) {
	valid := RunOptions{Automatic: true, Namespace: "tools", Source: "http://prometheus.tools.svc:9090"}
	require.True(t, valid.Valid())
	for _, change := range []func(*RunOptions){
		func(o *RunOptions) { o.ServiceAccount = "privileged" },
		func(o *RunOptions) { o.Source = "http://user:password@metrics.invalid" },
		func(o *RunOptions) { o.Source = "http://metrics.invalid?token=secret" },
		func(o *RunOptions) { o.Namespace = "*" },
		func(o *RunOptions) { o.Label = "cluster" },
		func(o *RunOptions) { o.Cluster = "blue" },
		func(o *RunOptions) { o.Automatic = false },
	} {
		o := valid
		change(&o)
		require.False(t, o.Valid())
	}
	valid.Label = "cluster"
	valid.Cluster = "blue"
	require.True(t, valid.Valid())
	valid.ServiceAccount = "default"
	require.True(t, valid.Valid())
}

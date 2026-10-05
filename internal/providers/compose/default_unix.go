//go:build !windows

package compose

const defaultHost = "unix:///var/run/docker.sock"

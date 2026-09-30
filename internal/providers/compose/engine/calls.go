package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spk/spk-ocular/internal/provider"
)

// ping asks GET /_ping (unversioned) and negotiates the version.
func (c *Client) ping(ctx context.Context) (Ping, error) {
	_, h, err := c.get(ctx, "", "/_ping", nil, c.lim.ErrorBytes)
	if err != nil {
		return Ping{}, err
	}
	p := Ping{APIVersion: h.Get("Api-Version"), OSType: h.Get("OSType")}
	if p.APIVersion == "" {
		return p, unsupported("the endpoint did not report a Docker Engine API version (is it a Docker Engine?)")
	}
	v, err := negotiate(p.APIVersion)
	if err != nil {
		return p, err
	}
	p.Version = v
	return p, nil
}

// Ping always goes on the wire; a successful one also fixes the version
// the client speaks (if it was not negotiated yet).
func (c *Client) Ping(ctx context.Context) (Ping, error) {
	p, err := c.ping(ctx)
	if err == nil {
		c.mu.Lock()
		if c.version == "" {
			c.version = p.Version
		}
		c.mu.Unlock()
	}
	return p, err
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var v Info
	err := c.getJSON(ctx, "/info", nil, c.lim.InspectBytes, &v)
	return v, err
}

// ListContainers lists all containers (running or not) matching f.
func (c *Client) ListContainers(ctx context.Context, f Filters) ([]ContainerSummary, error) {
	q := url.Values{"all": {"1"}}
	if err := setFilters(q, f); err != nil {
		return nil, err
	}
	var v []ContainerSummary
	err := c.getJSON(ctx, "/containers/json", q, c.lim.ListBytes, &v)
	return v, err
}

func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerInspect, error) {
	var v ContainerInspect
	raw, err := c.getRaw(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, c.lim.InspectBytes, &v)
	v.Raw = raw
	return v, err
}

func (c *Client) ListNetworks(ctx context.Context, f Filters) ([]Network, error) {
	q := url.Values{}
	if err := setFilters(q, f); err != nil {
		return nil, err
	}
	var v []Network
	err := c.getJSON(ctx, "/networks", q, c.lim.ListBytes, &v)
	return v, err
}

func (c *Client) InspectNetwork(ctx context.Context, id string) (Network, error) {
	var v Network
	raw, err := c.getRaw(ctx, "/networks/"+url.PathEscape(id), nil, c.lim.InspectBytes, &v)
	v.Raw = raw
	return v, err
}

func (c *Client) ListVolumes(ctx context.Context, f Filters) (VolumeList, error) {
	q := url.Values{}
	if err := setFilters(q, f); err != nil {
		return VolumeList{}, err
	}
	var v VolumeList
	err := c.getJSON(ctx, "/volumes", q, c.lim.ListBytes, &v)
	return v, err
}

func (c *Client) InspectVolume(ctx context.Context, name string) (Volume, error) {
	var v Volume
	raw, err := c.getRaw(ctx, "/volumes/"+url.PathEscape(name), nil, c.lim.InspectBytes, &v)
	v.Raw = raw
	return v, err
}

// ListImages lists top-level images (not intermediate layers) matching f.
func (c *Client) ListImages(ctx context.Context, f Filters) ([]ImageSummary, error) {
	q := url.Values{}
	if err := setFilters(q, f); err != nil {
		return nil, err
	}
	var v []ImageSummary
	err := c.getJSON(ctx, "/images/json", q, c.lim.ListBytes, &v)
	return v, err
}

func (c *Client) InspectImage(ctx context.Context, id string) (ImageInspect, error) {
	var v ImageInspect
	raw, err := c.getRaw(ctx, "/images/"+url.PathEscape(id)+"/json", nil, c.lim.InspectBytes, &v)
	v.Raw = raw
	return v, err
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, limit int64, into any) error {
	_, err := c.getRaw(ctx, path, q, limit, into)
	return err
}

// getRaw does a bounded versioned GET and decodes the answer into into,
// returning the body (nil on error).
func (c *Client) getRaw(ctx context.Context, path string, q url.Values, limit int64, into any) ([]byte, error) {
	body, err := c.getVersioned(ctx, path, q, limit)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return nil, &Error{Class: provider.ClassInternal, Message: fmt.Sprintf("cannot decode the Docker Engine's answer to %s: %v", path, err), Err: err}
	}
	return body, nil
}

func setFilters(q url.Values, f Filters) error {
	if len(f) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string][]string(f))
	if err != nil {
		return &Error{Class: provider.ClassInternal, Message: err.Error(), Err: err}
	}
	q.Set("filters", string(b))
	return nil
}

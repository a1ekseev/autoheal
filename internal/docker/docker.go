// Package docker adapts the Docker Engine API client to monitor.DockerAPI.
package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"

	"github.com/a1ekseev/autoheal/internal/monitor"
)

// restartTimeoutSeconds matches docker-py's Container.restart() default (t=10).
const restartTimeoutSeconds = 10

// Client lists and restarts containers through the Docker Engine API.
type Client struct {
	api *client.Client
}

// New connects using DOCKER_HOST & co. (default unix:///var/run/docker.sock)
// with API version negotiation.
func New() (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{api: api}, nil
}

// Close releases the underlying HTTP transport.
func (c *Client) Close() error {
	if err := c.api.Close(); err != nil {
		return fmt.Errorf("close docker client: %w", err)
	}
	return nil
}

// ListRunning returns running containers, like `docker ps`.
func (c *Client) ListRunning(ctx context.Context) ([]monitor.Container, error) {
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	out := make([]monitor.Container, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, monitor.Container{ID: s.ID, Name: containerName(s.ID, s.Names), Labels: s.Labels})
	}
	return out, nil
}

// Restart restarts a container, waiting up to 10s for it to stop.
func (c *Client) Restart(ctx context.Context, id string) error {
	timeout := restartTimeoutSeconds
	if _, err := c.api.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	return nil
}

// containerName picks the container's own name from the list API's Names,
// skipping legacy-link aliases ("/linker/alias"); it falls back to the ID.
func containerName(id string, names []string) string {
	for _, n := range names {
		if n = strings.TrimPrefix(n, "/"); n != "" && !strings.Contains(n, "/") {
			return n
		}
	}
	return id
}

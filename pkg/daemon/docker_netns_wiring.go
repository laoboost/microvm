package daemon

import (
	"context"
	"log/slog"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/pkg/docker"
)

// dockerDefaultBridge is the interface dockerd creates for SB_DOCKER_NETWORK's
// default "bridge" network. A custom network produces a br-<id> interface whose
// name isn't known before dockerd runs, so the IPv6 disable then relies on the
// host-wide all/default sysctls alone.
const dockerDefaultBridge = "docker0"

// wireDockerNetnsPool starts the image-agnostic pause-netns warm pool
// (SB_DOCKER_NETNS_POOL_ENABLED). Unlike the per-image warm pool it has no
// capacity gate: a pause container holds a netns and ~1MB of memory, so
// slots are not admission-relevant.
//
// The sandbox IPv6 hard-disable this function used to carry now runs earlier —
// before any chain work — in bootSandboxNetworkIsolation.
func wireDockerNetnsPool(ctx context.Context, cfg config.Config, logger *slog.Logger, dockerClient *docker.Client) (*docker.NetnsPool, error) {
	if !cfg.DockerNetnsPoolEnabled {
		return nil, nil
	}
	pool := dockerClient.StartNetnsPool(ctx, logger,
		cfg.DockerNetnsPoolDepth,
		cfg.DockerNetnsPoolPauseImage,
		cfg.DockerNetnsPoolRefillInterval)
	logger.Info("docker netns pool enabled",
		"depth", cfg.DockerNetnsPoolDepth,
		"pause_image", cfg.DockerNetnsPoolPauseImage,
		"refill_interval", cfg.DockerNetnsPoolRefillInterval)
	return pool, nil
}

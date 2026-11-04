package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/pkg/docker"
	"github.com/aerol-ai/microvm/pkg/models"
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

// wireDockerOwnPause readies the pause container a restricted Docker create
// starts for itself when the pool has no slot for it (pkg/docker
// netns_pause_on_demand.go): the image it runs, and, on a docker-engine
// node without the pool, a background pull of that image and a sweep of the
// pauses an earlier run left behind. The pool's own loop does both when it
// runs. Off the boot path: nothing waits for it. Reports whether it started
// the background work.
func wireDockerOwnPause(ctx context.Context, cfg config.Config, dockerClient *docker.Client, rulesOn, poolOn bool, bootAt time.Time) bool {
	dockerClient.SetNetnsPauseImage(cfg.DockerNetnsPoolPauseImage)
	if poolOn || !rulesOn || cfg.ContainerEngine == models.ContainerEngineContainerd {
		return false
	}
	go dockerClient.PrepareNetnsPause(ctx, bootAt)
	return true
}

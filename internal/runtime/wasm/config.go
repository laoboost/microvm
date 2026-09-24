package wasm

import (
	"time"

	"github.com/aerol-ai/microvm/internal/config"
)

// Config holds host-side WASM runtime settings.
type Config struct {
	RunDir             string
	ModulesDir         string
	DefaultMemoryMB    int
	DefaultWallTimeout time.Duration
	DrainTimeout       time.Duration
	// ResidentHostEnabled routes non-listen creates to a shared resident
	// compile-once/instantiate-many host per (ownerRef, digest, memoryMB) bucket
	// instead of a fresh per-sandbox worker (plans/wasm-resident-module-host.md).
	// Default false; requires a resident supervisor wired via SetResidentHostSupervisor.
	ResidentHostEnabled bool
	// ResidentHostMaxInstances caps co-tenants per host process; 0 = unbounded.
	// When full, the driver spills to <bucket>-2.sock (SB_WASM_RESIDENT_HOST_MAX_INSTANCES).
	ResidentHostMaxInstances int
	// ResidentHostIdleTTL reaps a resident host holding zero instances for this
	// long; 0 disables the reaper (SB_WASM_RESIDENT_HOST_IDLE_TTL).
	ResidentHostIdleTTL time.Duration
	// ToolboxHostExecEnabled turns on the in-process toolbox routes that spawn a
	// real host process. Default false in production: the wasm runtime has no
	// process jail, so those routes fail closed with 501.
	ToolboxHostExecEnabled bool
}

// FromDaemonConfig projects the WASM slice of daemon config into driver config.
func FromDaemonConfig(cfg config.Config) Config {
	return Config{
		RunDir:                   cfg.WasmRunDir,
		ModulesDir:               cfg.WasmModulesDir,
		DefaultMemoryMB:          cfg.WasmDefaultMemoryMB,
		DefaultWallTimeout:       cfg.WasmDefaultTimeout,
		DrainTimeout:             cfg.WasmDrainTimeout,
		ResidentHostEnabled:      cfg.WasmResidentHostEnabled,
		ResidentHostMaxInstances: cfg.WasmResidentHostMaxInstances,
		ResidentHostIdleTTL:      cfg.WasmResidentHostIdleTTL,
	}
}

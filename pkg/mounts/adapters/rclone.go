package adapters

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aerol-ai/microvm/pkg/models"
)

// rcloneNetworkBackends is the allowlist of rclone backends that reach storage
// over the network only. `rclone mount --config` runs as root with the
// tenant-supplied conf, so any backend able to address the HOST filesystem must
// be refused. A deny-list is not enough: it missed `union` (whose `upstreams`
// points at host paths) and every quoted/`:`-spelled spelling of `local`.
//
// local, memory and every wrapper backend that can chain to one (alias, cache,
// chunker, combine, compress, crypt, hasher, union) are deliberately absent —
// the allowlist is the security boundary, so an unknown type fails closed.
var rcloneNetworkBackends = map[string]struct{}{
	"s3": {}, "swift": {}, "b2": {}, "azureblob": {}, "azurefiles": {},
	"gcs": {}, "googlecloudstorage": {}, "drive": {}, "dropbox": {},
	"onedrive": {}, "box": {}, "ftp": {}, "sftp": {}, "http": {},
	"webdav": {}, "mega": {}, "hdfs": {}, "smb": {}, "sugarsync": {},
	"opendrive": {}, "jottacloud": {}, "koofr": {}, "pcloud": {},
	"premiumizeme": {}, "putio": {}, "seafile": {}, "sia": {}, "storj": {},
	"tardigrade": {}, "uptobox": {}, "internetarchive": {}, "zoho": {},
	"yandex": {}, "qingstor": {}, "qingcloud": {}, "mailru": {}, "ulozto": {},
	"fichier": {}, "filefabric": {}, "pikpak": {}, "sharefile": {},
	"netstorage": {}, "oracleobjectstorage": {}, "oos": {}, "hidrive": {},
}

// parseRcloneRemotes extracts each remote's backend type from an INI-shaped
// rclone.conf. It is deliberately stricter than rclone's own goconfig parser:
// it accepts only bare `key = value` lines with unquoted keys and sections, so
// a conf cannot smuggle a backend past the allowlist with quoting or a `:`
// separator. Anything it cannot parse — a non-`=` separator, a quoted key, a
// duplicate key or section — is an error rather than being silently ignored.
// Global (section-less) keys are parsed for syntax only and do not define a
// remote.
func parseRcloneRemotes(conf string) (map[string]string, error) {
	remotes := make(map[string]string)
	seenKey := make(map[string]struct{})
	section := ""
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") || len(line) < 3 {
				return nil, fmt.Errorf("rclone conf: malformed section header %q", line)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" || strings.ContainsAny(name, "[]\"'`") {
				return nil, fmt.Errorf("rclone conf: invalid section name %q", line)
			}
			if _, dup := remotes[name]; dup {
				return nil, fmt.Errorf("rclone conf: duplicate section %q", name)
			}
			remotes[name] = ""
			seenKey = make(map[string]struct{})
			section = name
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("rclone conf: unparseable line %q (expected key = value)", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || strings.ContainsAny(key, " \t\"'`") {
			return nil, fmt.Errorf("rclone conf: quoted or invalid key in line %q", line)
		}
		normKey := strings.ToLower(key)
		if section == "" {
			continue
		}
		if _, dup := seenKey[normKey]; dup {
			return nil, fmt.Errorf("rclone conf: duplicate key %q in section %q", key, section)
		}
		seenKey[normKey] = struct{}{}
		switch normKey {
		case "type":
			remotes[section] = value
		case "remote", "upstreams":
			// Only wrapper backends (alias, crypt, cache, union, combine,
			// chunker, compress, hasher, ...) declare these. The mounted
			// remote could chain to a host-FS backend, so refuse the whole
			// conf whenever one appears.
			return nil, fmt.Errorf("rclone conf: remote %q declares a %q field; wrapper backends are not allowed", section, key)
		}
	}
	return remotes, nil
}

// Rclone mounts any rclone-supported remote using the user-supplied
// rclone.conf as a credentials file. The config is unlinked after the mount
// becomes ready; rclone keeps it in memory.
type Rclone struct{}

func (Rclone) Build(sandboxID string, index int, spec models.MountSpec, hostTarget, credDir string) (Plan, error) {
	conf := spec.Credentials["rclone_conf"]
	if conf == "" {
		return Plan{}, errors.New("rclone requires credentials.rclone_conf")
	}
	if err := checkSource("rclone", spec.Source); err != nil {
		return Plan{}, err
	}
	remotes, err := parseRcloneRemotes(conf)
	if err != nil {
		return Plan{}, err
	}
	for name, remoteType := range remotes {
		if _, allowed := rcloneNetworkBackends[strings.ToLower(remoteType)]; !allowed {
			return Plan{}, fmt.Errorf("rclone remote %q uses non-network backend type %q", name, remoteType)
		}
	}
	remote, _, ok := strings.Cut(spec.Source, ":")
	if !ok || remote == "" {
		return Plan{}, fmt.Errorf("rclone source must look like remote:path: %q", spec.Source)
	}
	if _, defined := remotes[remote]; !defined {
		return Plan{}, fmt.Errorf("rclone source remote %q is not defined with a backend type in credentials.rclone_conf", remote)
	}

	credFile := filepath.Join(credDir, fmt.Sprintf("%s-%d.rclone.conf", sandboxID, index))

	argv := []string{
		"rclone", "mount",
		"--config", credFile,
		"--vfs-cache-mode", valueOr(spec.Options["vfs_cache_mode"], "writes"),
		spec.Source, hostTarget,
	}
	if spec.ReadOnly {
		argv = append(argv, "--read-only")
	}

	return Plan{
		Argv:       argv,
		CredFile:   credFile,
		CredBody:   []byte(conf),
		UnlinkCred: true,
	}, nil
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

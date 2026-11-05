package webui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var workerReleaseTag = regexp.MustCompile(`^v[0-9][0-9A-Za-z.+-]*$`)
var errWorkerReleaseAPI = errors.New("GitHub release API unavailable")

// shortcut: downloads are capped at 64 MiB; raise if future worker releases outgrow it.
const maxWorkerBinaryBytes = 64 << 20

type workerManifest struct {
	Tag        string `json:"tag"`
	SHA256     string `json:"sha256"`
	Arch       string `json:"architecture"`
	Prerelease *bool  `json:"prerelease,omitempty"`
}

type githubWorkerRelease struct {
	Tag         string    `json:"tag_name"`
	Draft       *bool     `json:"draft"`
	Prerelease  *bool     `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

type workerReleases struct {
	client         *http.Client
	api, downloads string
	arch           string
}

func prepareWorker(ctx context.Context) (string, *os.File, error) {
	if bin := os.Getenv("WORKER_BIN"); bin != "" {
		return bin, nil, nil
	}
	return (workerReleases{
		client:    &http.Client{Timeout: 2 * time.Minute},
		api:       "https://api.github.com/repos/blestafist/LingosBot",
		downloads: "https://github.com/blestafist/LingosBot/releases/download",
		arch:      runtime.GOARCH,
	}).prepare(ctx, ".data/lingosbot", "/usr/local/share/lingosbot", env("RELEASE_VERSION", "latest"))
}

// The returned lock stays held for the server's lifetime, not only during download.
func (s workerReleases) prepare(ctx context.Context, path, seedDir, version string) (string, *os.File, error) {
	if s.arch != "amd64" && s.arch != "arm64" {
		return "", nil, errors.New("managed worker supports only linux/amd64 and linux/arm64; set WORKER_BIN")
	}
	if version != "latest" && version != "latest-prerelease" && !workerReleaseTag.MatchString(version) {
		return "", nil, errors.New("RELEASE_VERSION must be v..., latest or latest-prerelease")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return "", nil, errors.New("another WebUI process is using the managed worker")
	}
	if err := s.update(ctx, path, seedDir, version); err != nil {
		lock.Close()
		return "", nil, fmt.Errorf("prepare managed worker: %w", err)
	}
	return path, lock, nil
}

func verifiedWorker(path, manifestPath, arch string) (workerManifest, error) {
	var manifest workerManifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	expected, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(expected) != sha256.Size || !workerReleaseTag.MatchString(manifest.Tag) || manifest.Arch != arch {
		return manifest, errors.New("invalid worker manifest")
	}
	binary, err := os.Open(path)
	if err != nil {
		return manifest, err
	}
	defer binary.Close()
	info, err := binary.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return manifest, errors.New("worker is not a regular executable")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, binary); err != nil {
		return manifest, err
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), manifest.SHA256) {
		return manifest, errors.New("worker SHA-256 mismatch")
	}
	return manifest, nil
}

func (s workerReleases) update(ctx context.Context, path, seedDir, version string) error {
	cached, cacheErr := verifiedWorker(path, path+".release.json", s.arch)
	if cacheErr == nil && cached.Tag == version {
		return nil // Fixed tags need no network on ordinary restarts.
	}
	seedPath := filepath.Join(seedDir, "lingosbot")
	seed, seedErr := verifiedWorker(seedPath, filepath.Join(seedDir, "worker-release.json"), s.arch)
	tag := version
	var release githubWorkerRelease
	// A verified Docker seed can bootstrap a pinned tag without a second download.
	if seedErr != nil || seed.Tag != version {
		var err error
		release, err = s.resolve(ctx, version)
		if err != nil {
			if !errors.Is(err, errWorkerReleaseAPI) || ctx.Err() != nil ||
				(version != "latest" && version != "latest-prerelease") {
				return err
			}
			prerelease := version == "latest-prerelease"
			if cacheErr == nil && cached.Prerelease != nil && *cached.Prerelease == prerelease {
				log.Printf("WARNING: %s: %v; using verified cached worker %s", version, err, cached.Tag)
				return nil
			}
			if seedErr != nil || seed.Prerelease == nil || *seed.Prerelease != prerelease {
				return err
			}
			log.Printf("WARNING: %s: %v; using verified seed worker %s", version, err, seed.Tag)
			tag = seed.Tag
		} else {
			tag = release.Tag
			if cacheErr == nil && cached.Tag == tag {
				return nil
			}
		}
	}
	if seedErr == nil && seed.Tag == tag {
		binary, err := os.Open(seedPath)
		if err != nil {
			return err
		}
		defer binary.Close()
		return installWorker(path, binary, seed)
	}
	assetArch := map[string]string{"amd64": "x64", "arm64": "arm64"}[s.arch]
	name := "lingosbot-worker-linux-" + assetArch
	for _, required := range []string{name, name + ".sha256"} {
		count := 0
		for _, asset := range release.Assets {
			if asset.Name == required {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("release %s is missing a unique asset: %s", tag, required)
		}
	}
	// Only fixed-tag URLs are used, even if a channel changes during download.
	assetURL := s.downloads + "/" + url.PathEscape(tag) + "/" + name
	response, err := s.get(ctx, assetURL+".sha256")
	if err != nil {
		return err
	}
	checksum, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	response.Body.Close()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
		return errors.New("invalid worker SHA-256 file or filename")
	}
	expected, err := hex.DecodeString(fields[0])
	if err != nil || len(expected) != sha256.Size {
		return errors.New("invalid worker SHA-256 digest")
	}
	response, err = s.get(ctx, assetURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return installWorker(path, response.Body, workerManifest{Tag: tag, SHA256: fields[0], Arch: s.arch, Prerelease: release.Prerelease})
}

func (s workerReleases) get(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "LingosBot-WebUI")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GitHub request failed: HTTP %d (%s)", response.StatusCode, address)
	}
	return response, nil
}

func (s workerReleases) json(ctx context.Context, path string, target any) error {
	response, err := s.get(ctx, s.api+path)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target)
}

func (s workerReleases) resolve(ctx context.Context, version string) (githubWorkerRelease, error) {
	var selected githubWorkerRelease
	if workerReleaseTag.MatchString(version) {
		if err := s.json(ctx, "/releases/tags/"+url.PathEscape(version), &selected); err != nil {
			return selected, fmt.Errorf("%w: %w", errWorkerReleaseAPI, err)
		}
		if selected.Tag != version {
			return selected, errors.New("GitHub returned a different release tag")
		}
	} else {
		for page := 1; ; page++ {
			var batch []githubWorkerRelease
			if err := s.json(ctx, fmt.Sprintf("/releases?per_page=100&page=%d", page), &batch); err != nil {
				return selected, fmt.Errorf("%w: %w", errWorkerReleaseAPI, err)
			}
			for _, release := range batch {
				if release.Draft != nil && !*release.Draft && release.Prerelease != nil &&
					*release.Prerelease == (version == "latest-prerelease") &&
					release.PublishedAt.After(selected.PublishedAt) {
					selected = release
				}
			}
			if len(batch) < 100 {
				break
			}
		}
	}
	if selected.Draft == nil || *selected.Draft || selected.PublishedAt.IsZero() ||
		!workerReleaseTag.MatchString(selected.Tag) {
		return selected, fmt.Errorf("no published release matches %s (drafts excluded)", version)
	}
	return selected, nil
}

func installWorker(path string, source io.Reader, manifest workerManifest) error {
	stage, err := os.MkdirTemp(filepath.Dir(path), ".worker-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	binaryPath := filepath.Join(stage, "lingosbot")
	binary, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	defer binary.Close()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(binary, digest), io.LimitReader(source, maxWorkerBinaryBytes+1))
	if err != nil {
		return err
	}
	if written > maxWorkerBinaryBytes {
		return errors.New("worker binary exceeds 64 MiB size limit; previous worker kept")
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), manifest.SHA256) {
		return errors.New("worker SHA-256 mismatch; previous worker kept")
	}
	if err := binary.Sync(); err != nil {
		return err
	}
	if err := binary.Close(); err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(stage, "release.json")
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(binaryPath, path); err != nil {
		return err
	}
	// A crash between renames is detected by the manifest checksum on next startup.
	if err := os.Rename(manifestPath, path+".release.json"); err != nil {
		return err
	}
	log.Printf("Managed worker %s installed at %s", manifest.Tag, path)
	return nil
}

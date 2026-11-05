package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type workerTestRoute struct {
	status int
	body   []byte
	after  func()
}

type workerTestTransport func(*http.Request) (*http.Response, error)

func (f workerTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type workerReleaseTest struct {
	t          *testing.T
	mu         sync.Mutex
	routes     map[string]workerTestRoute
	requests   []string
	source     workerReleases
	root, path string
	seed       string
}

func newWorkerReleaseTest(t *testing.T) *workerReleaseTest {
	t.Helper()
	f := &workerReleaseTest{t: t, routes: map[string]workerTestRoute{}, root: t.TempDir()}
	f.path = filepath.Join(f.root, ".data", "lingosbot")
	f.seed = filepath.Join(f.root, "seed")
	for _, name := range []string{"webui.db", "webui.db-wal", "webui.db-shm"} {
		path := filepath.Join(f.root, name)
		before := []byte("existing encrypted data: " + name)
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if data, err := os.ReadFile(path); err != nil || string(data) != string(before) {
				t.Errorf("persistent database changed: %s, %v", name, err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.RequestURI())
		route, ok := f.routes[r.URL.RequestURI()]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if route.after != nil {
			route.after()
		}
		w.WriteHeader(route.status)
		_, _ = w.Write(route.body)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	transport := client.Transport
	client.Transport = workerTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme+"://"+r.URL.Host != server.URL {
			return nil, fmt.Errorf("external network forbidden: %s", r.URL.Host)
		}
		return transport.RoundTrip(r)
	})
	f.source = workerReleases{client: client, api: server.URL + "/api", downloads: server.URL + "/download", arch: "amd64"}
	return f
}

func (f *workerReleaseTest) set(path string, status int, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = workerTestRoute{status: status, body: body}
}

func (f *workerReleaseTest) json(path string, document any) {
	f.t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		f.t.Fatal(err)
	}
	f.set(path, http.StatusOK, data)
}

func (f *workerReleaseTest) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.requests...)
}

func (f *workerReleaseTest) release(tag string, prerelease, draft bool, published string) map[string]any {
	var assets []map[string]string
	for _, arch := range []string{"x64", "arm64"} {
		name := "lingosbot-worker-linux-" + arch
		body := []byte(tag + "/" + name)
		digest := sha256.Sum256(body)
		f.set("/download/"+tag+"/"+name, 200, body)
		f.set("/download/"+tag+"/"+name+".sha256", 200, fmt.Appendf(nil, "%x  %s\n", digest, name))
		assets = append(assets, map[string]string{"name": name}, map[string]string{"name": name + ".sha256"})
	}
	var date any = published
	if published == "" {
		date = nil
	}
	release := map[string]any{"tag_name": tag, "prerelease": prerelease, "draft": draft, "published_at": date, "assets": assets}
	f.json("/api/releases/tags/"+tag, release)
	return release
}

func (f *workerReleaseTest) installed(tag, arch, content string, prerelease ...bool) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
		f.t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	manifest := workerManifest{Tag: tag, Arch: arch, SHA256: hex.EncodeToString(digest[:])}
	if len(prerelease) != 0 {
		manifest.Prerelease = &prerelease[0]
	}
	if err := installWorker(f.path, strings.NewReader(content), manifest); err != nil {
		f.t.Fatal(err)
	}
}

func (f *workerReleaseTest) seeded(tag, content string, prerelease *bool) {
	f.t.Helper()
	if err := os.MkdirAll(f.seed, 0755); err != nil {
		f.t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	manifest, err := json.Marshal(workerManifest{Tag: tag, Arch: f.source.arch, SHA256: hex.EncodeToString(digest[:]), Prerelease: prerelease})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.seed, "lingosbot"), []byte(content), 0755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.seed, "worker-release.json"), manifest, 0644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *workerReleaseTest) apiFailure(failure string) string {
	f.t.Helper()
	switch failure {
	case "transport":
		transport := f.source.client.Transport
		f.source.client.Transport = workerTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				return nil, errors.New("simulated API outage")
			}
			return transport.RoundTrip(r)
		})
		return "simulated API outage"
	case "invalid-json":
		f.set("/api/releases?per_page=100&page=1", 200, []byte("{invalid"))
		return "invalid character"
	case "403":
		f.set("/api/releases?per_page=100&page=1", 403, nil)
		return "HTTP 403"
	case "503":
		f.set("/api/releases?per_page=100&page=1", 503, nil)
		return "HTTP 503"
	default:
		f.t.Fatalf("unknown failure: %s", failure)
		return ""
	}
}

func captureWorkerLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func (f *workerReleaseTest) prepare(version string) string {
	f.t.Helper()
	path, lock, err := f.source.prepare(context.Background(), f.path, f.seed, version)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		f.t.Fatal(err)
	}
	if path != f.path {
		f.t.Fatalf("worker path = %s, want %s", path, f.path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := verifiedWorker(path, path+".release.json", f.source.arch); err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func TestWorkerReleaseSeedAndPinnedRestartsNeedNoNetwork(t *testing.T) {
	f := newWorkerReleaseTest(t)
	body := []byte("immutable Docker seed")
	f.seeded("v2.0.0", string(body), nil)
	for range 2 {
		if got := f.prepare("v2.0.0"); got != string(body) {
			t.Fatalf("installed seed = %q", got)
		}
	}
	if len(f.paths()) != 0 {
		t.Fatalf("pinned restart downloaded: %v", f.paths())
	}
	if info, err := os.Stat(f.path); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("worker permissions: %v, %v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.seed, "lingosbot")); err != nil || string(data) != string(body) {
		t.Fatal("Docker seed changed")
	}
}

func TestWorkerReleaseBothArchitecturesAndImmutableTag(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			f := newWorkerReleaseTest(t)
			f.source.arch = arch
			f.release("v2.0.0-rc.1", true, false, "2026-11-04T22:31:51Z")
			asset := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
			if got := f.prepare("v2.0.0-rc.1"); got != "v2.0.0-rc.1/lingosbot-worker-linux-"+asset {
				t.Fatal(got)
			}
			manifest, err := verifiedWorker(f.path, f.path+".release.json", arch)
			if err != nil || manifest.Prerelease == nil || !*manifest.Prerelease {
				t.Fatalf("downloaded worker has no prerelease metadata: %+v, %v", manifest, err)
			}
			before := f.paths()
			f.prepare("v2.0.0-rc.1")
			if !reflect.DeepEqual(before, f.paths()) {
				t.Fatal("fixed tag restart made requests")
			}
		})
	}
}

func TestWorkerReleaseChannelsPublicationAndPagination(t *testing.T) {
	f := newWorkerReleaseTest(t)
	old := f.release("v9.0.0", false, false, "2026-10-01T00:00:00Z")
	stable := f.release("v1.2.1-Stable", false, false, "2026-11-01T00:00:00Z")
	pre := f.release("v2.0.0", true, false, "2026-11-04T22:31:51Z")
	draft := f.release("v99.0.0", false, true, "2026-11-05T00:00:00Z")
	draftPre := f.release("v99.0.0-rc.1", true, true, "2026-11-05T00:00:00Z")
	unpublished := f.release("v100.0.0", false, false, "")
	page := make([]map[string]any, 100)
	for i := range page {
		page[i] = old
	}
	f.json("/api/releases?per_page=100&page=1", page)
	f.json("/api/releases?per_page=100&page=2", []map[string]any{pre, stable, draft, draftPre, unpublished})
	for channel, want := range map[string]string{"latest": "v1.2.1-Stable", "latest-prerelease": "v2.0.0"} {
		got, err := f.source.resolve(context.Background(), channel)
		if err != nil || got.Tag != want {
			t.Fatalf("%s = %s, %v; want %s", channel, got.Tag, err, want)
		}
	}
}

func TestWorkerReleaseChannelCheckedAtStartupWithoutRedownloading(t *testing.T) {
	f := newWorkerReleaseTest(t)
	first := f.release("v1.0.0", false, false, "2026-11-01T00:00:00Z")
	second := f.release("v2.0.0", false, false, "2026-11-02T00:00:00Z")
	f.json("/api/releases?per_page=100&page=1", []map[string]any{first})
	f.mu.Lock()
	route := f.routes["/download/v1.0.0/lingosbot-worker-linux-x64.sha256"]
	route.after = func() { f.json("/api/releases?per_page=100&page=1", []map[string]any{second, first}) }
	f.routes["/download/v1.0.0/lingosbot-worker-linux-x64.sha256"] = route
	f.mu.Unlock()
	if got := f.prepare("latest"); !strings.HasPrefix(got, "v1.0.0/") {
		t.Fatal("channel changed the resolved tag during installation:", got)
	}
	if got := f.prepare("latest"); !strings.HasPrefix(got, "v2.0.0/") {
		t.Fatal("next startup did not update:", got)
	}
	before := f.paths()
	f.prepare("latest")
	paths := f.paths()[len(before):]
	if !reflect.DeepEqual(paths, []string{"/api/releases?per_page=100&page=1"}) {
		t.Fatalf("unchanged channel redownloaded binaries: %v", paths)
	}
	manifest, err := verifiedWorker(f.path, f.path+".release.json", "amd64")
	if err != nil || manifest.Prerelease == nil || *manifest.Prerelease {
		t.Fatalf("downloaded stable worker has wrong channel metadata: %+v, %v", manifest, err)
	}
}

func TestWorkerReleaseChannelAPIErrorsReuseOnlyMatchingVerifiedCache(t *testing.T) {
	for _, channel := range []string{"latest", "latest-prerelease"} {
		for _, failure := range []string{"403", "503", "transport", "invalid-json"} {
			t.Run(channel+"/"+failure, func(t *testing.T) {
				f := newWorkerReleaseTest(t)
				f.installed("v2.0.0", "amd64", "cached worker", channel == "latest-prerelease")
				before, _ := os.ReadFile(f.path + ".release.json")
				reason := f.apiFailure(failure)
				output := captureWorkerLog(t)
				if got := f.prepare(channel); got != "cached worker" {
					t.Fatal(got)
				}
				after, _ := os.ReadFile(f.path + ".release.json")
				if !bytes.Equal(before, after) {
					t.Fatal("offline cache reuse rewrote its manifest")
				}
				for _, want := range []string{"WARNING:", "verified cached worker v2.0.0", channel, reason} {
					if !strings.Contains(output.String(), want) {
						t.Fatalf("missing %q in fallback warning: %s", want, output)
					}
				}
				paths := f.paths()
				if failure == "transport" {
					if len(paths) != 0 {
						t.Fatal(paths)
					}
				} else if !reflect.DeepEqual(paths, []string{"/api/releases?per_page=100&page=1"}) {
					t.Fatalf("fallback downloaded binaries: %v", paths)
				}
			})
		}
	}
}

func TestWorkerReleaseChannelAPIErrorsUseMatchingSeed(t *testing.T) {
	for _, channel := range []string{"latest", "latest-prerelease"} {
		for _, cached := range []string{"other-channel", "legacy-same-tag"} {
			t.Run(channel+"/"+cached, func(t *testing.T) {
				f := newWorkerReleaseTest(t)
				prerelease := channel == "latest-prerelease"
				if cached == "other-channel" {
					f.installed("v1.0.0", "amd64", "other-channel cache", !prerelease)
				} else {
					f.installed("v2.0.0", "amd64", "legacy cache")
				}
				f.seeded("v2.0.0", "matching-channel Docker seed", &prerelease)
				f.apiFailure("503")
				output := captureWorkerLog(t)
				if got := f.prepare(channel); got != "matching-channel Docker seed" {
					t.Fatal(got)
				}
				manifest, err := verifiedWorker(f.path, f.path+".release.json", "amd64")
				if err != nil || manifest.Prerelease == nil || *manifest.Prerelease != prerelease {
					t.Fatalf("installed seed lost channel metadata: %+v, %v", manifest, err)
				}
				for _, want := range []string{"WARNING:", channel, "HTTP 503", "verified seed worker v2.0.0"} {
					if !strings.Contains(output.String(), want) {
						t.Fatalf("missing %q in fallback warning: %s", want, output)
					}
				}
				if paths := f.paths(); !reflect.DeepEqual(paths, []string{"/api/releases?per_page=100&page=1"}) {
					t.Fatalf("seed fallback downloaded binaries: %v", paths)
				}
			})
		}
	}
}

func TestWorkerReleaseOfflineRejectsUnknownWrongChannelOrUnverifiedWorkers(t *testing.T) {
	for _, source := range []string{"cache", "seed"} {
		for _, channel := range []string{"latest", "latest-prerelease"} {
			for _, problem := range []string{"wrong-channel", "unknown-channel", "corrupt", "wrong-architecture"} {
				t.Run(source+"/"+channel+"/"+problem, func(t *testing.T) {
					f := newWorkerReleaseTest(t)
					prerelease := channel == "latest-prerelease"
					if problem == "wrong-channel" {
						prerelease = !prerelease
					}
					known := &prerelease
					if problem == "unknown-channel" {
						known = nil
					}
					arch := "amd64"
					if problem == "wrong-architecture" {
						arch = "arm64"
					}
					binaryPath := f.path
					if source == "cache" {
						if known == nil {
							f.installed("v2.0.0", arch, "existing worker")
						} else {
							f.installed("v2.0.0", arch, "existing worker", *known)
						}
					} else {
						f.source.arch = arch
						f.seeded("v2.0.0", "existing worker", known)
						f.source.arch = "amd64"
						binaryPath = filepath.Join(f.seed, "lingosbot")
					}
					if problem == "corrupt" {
						if err := os.WriteFile(binaryPath, []byte("corrupted worker"), 0700); err != nil {
							t.Fatal(err)
						}
					}
					f.apiFailure("403")
					output := captureWorkerLog(t)
					if _, lock, err := f.source.prepare(context.Background(), f.path, f.seed, channel); !errors.Is(err, errWorkerReleaseAPI) || lock != nil {
						t.Fatalf("untrusted fallback accepted: lock=%v, err=%v", lock, err)
					}
					if strings.Contains(output.String(), "WARNING:") {
						t.Fatal("fallback warning despite refusing fallback:", output)
					}
				})
			}
		}
	}
}

func TestWorkerReleaseChannelCancellationDoesNotFallBack(t *testing.T) {
	for _, channel := range []string{"latest", "latest-prerelease"} {
		t.Run(channel, func(t *testing.T) {
			f := newWorkerReleaseTest(t)
			prerelease := channel == "latest-prerelease"
			f.installed("v1.0.0", "amd64", "cached worker", prerelease)
			f.seeded("v2.0.0", "Docker seed", &prerelease)
			output := captureWorkerLog(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, lock, err := f.source.prepare(ctx, f.path, f.seed, channel); !errors.Is(err, context.Canceled) || lock != nil {
				t.Fatalf("cancelled startup accepted: lock=%v, err=%v", lock, err)
			}
			if strings.Contains(output.String(), "WARNING:") {
				t.Fatal("cancelled startup fell back:", output)
			}
			if body, _ := os.ReadFile(f.path); string(body) != "cached worker" {
				t.Fatal("cancellation changed the worker")
			}
		})
	}
}

func TestWorkerReleaseChannelSemanticAndDownloadErrorsNeverFallBack(t *testing.T) {
	for _, channel := range []string{"latest", "latest-prerelease"} {
		for _, failure := range []string{"no-match", "invalid-tag", "missing-asset", "checksum", "checksum-file", "download-503"} {
			t.Run(channel+"/"+failure, func(t *testing.T) {
				f := newWorkerReleaseTest(t)
				prerelease := channel == "latest-prerelease"
				f.installed("v1.0.0", "amd64", "cached worker", prerelease)
				f.seeded("v1.0.0", "cached worker", &prerelease)
				before, _ := os.ReadFile(f.path + ".release.json")
				release := f.release("v2.0.0", prerelease, false, "2026-11-04T22:31:51Z")
				batch := []map[string]any{release}
				switch failure {
				case "no-match":
					batch = nil
				case "invalid-tag":
					release["tag_name"] = "not-a-release-tag"
				case "missing-asset":
					release["assets"] = nil
				case "checksum":
					f.set("/download/v2.0.0/lingosbot-worker-linux-x64", 200, []byte("corrupted download"))
				case "checksum-file":
					f.set("/download/v2.0.0/lingosbot-worker-linux-x64.sha256", 200, []byte("invalid checksum"))
				case "download-503":
					f.set("/download/v2.0.0/lingosbot-worker-linux-x64", 503, nil)
				}
				f.json("/api/releases?per_page=100&page=1", batch)
				output := captureWorkerLog(t)
				_, lock, err := f.source.prepare(context.Background(), f.path, f.seed, channel)
				if err == nil || errors.Is(err, errWorkerReleaseAPI) || lock != nil {
					t.Fatalf("semantic/download error allowed API fallback: lock=%v, err=%v", lock, err)
				}
				if strings.Contains(output.String(), "WARNING:") {
					t.Fatal("semantic/download failure fell back:", output)
				}
				after, _ := os.ReadFile(f.path + ".release.json")
				if body, _ := os.ReadFile(f.path); string(body) != "cached worker" || !bytes.Equal(before, after) {
					t.Fatal("failed update changed cached files")
				}
			})
		}
	}
}

type workerOversizeReader struct{ read int64 }

func (r *workerOversizeReader) Read(p []byte) (int, error) {
	clear(p)
	r.read += int64(len(p))
	return len(p), nil
}

func TestWorkerReleaseOversizeDownloadIsBoundedAndPreservesFiles(t *testing.T) {
	f := newWorkerReleaseTest(t)
	f.installed("v1.0.0", "amd64", "cached worker")
	before, _ := os.ReadFile(f.path + ".release.json")
	source := &workerOversizeReader{}
	err := installWorker(f.path, source, workerManifest{Tag: "v2.0.0", Arch: "amd64", SHA256: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "exceeds 64 MiB size limit") {
		t.Fatalf("oversize error = %v", err)
	}
	if source.read != maxWorkerBinaryBytes+1 {
		t.Fatalf("unbounded download: read %d bytes", source.read)
	}
	after, _ := os.ReadFile(f.path + ".release.json")
	if binary, _ := os.ReadFile(f.path); string(binary) != "cached worker" || !bytes.Equal(before, after) {
		t.Fatal("oversize download changed cached files")
	}
	if stages, _ := filepath.Glob(filepath.Join(filepath.Dir(f.path), ".worker-*")); len(stages) != 0 {
		t.Fatalf("oversize download leaked temporary files: %v", stages)
	}
}

func TestWorkerReleaseFailuresPreservePreviousWorkerAndDatabase(t *testing.T) {
	for _, failure := range []string{"checksum", "filename", "digest", "missing-asset", "duplicate-asset", "download-404", "api-403", "draft", "different-tag", "no-match", "invalid-json", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			f := newWorkerReleaseTest(t)
			f.installed("v0.1.0", "amd64", "previous worker")
			before, _ := os.ReadFile(f.path + ".release.json")
			release := f.release("v2.0.0", true, false, "2026-11-04T22:31:51Z")
			version := "v2.0.0"
			ctx := context.Background()
			switch failure {
			case "checksum":
				f.set("/download/v2.0.0/lingosbot-worker-linux-x64", 200, []byte("tampered"))
			case "filename":
				f.set("/download/v2.0.0/lingosbot-worker-linux-x64.sha256", 200, []byte(strings.Repeat("a", 64)+"  ../../webui.db\n"))
			case "digest":
				f.set("/download/v2.0.0/lingosbot-worker-linux-x64.sha256", 200, []byte("bad  lingosbot-worker-linux-x64\n"))
			case "missing-asset":
				release["assets"] = []map[string]string{}
			case "duplicate-asset":
				assets := release["assets"].([]map[string]string)
				release["assets"] = append(assets, assets[0])
			case "download-404":
				f.set("/download/v2.0.0/lingosbot-worker-linux-x64", 404, nil)
			case "draft":
				release["draft"] = true
			case "different-tag":
				release["tag_name"] = "v3.0.0"
			case "no-match":
				version = "latest"
				f.json("/api/releases?per_page=100&page=1", []map[string]any{release})
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			f.json("/api/releases/tags/v2.0.0", release)
			if failure == "api-403" {
				f.set("/api/releases/tags/v2.0.0", 403, nil)
			}
			if failure == "invalid-json" {
				f.set("/api/releases/tags/v2.0.0", 200, []byte("{invalid"))
			}
			if _, lock, err := f.source.prepare(ctx, f.path, f.seed, version); err == nil || lock != nil {
				t.Fatalf("failed update returned lock=%v, err=%v", lock, err)
			}
			after, _ := os.ReadFile(f.path + ".release.json")
			binary, _ := os.ReadFile(f.path)
			if string(binary) != "previous worker" || string(before) != string(after) {
				t.Fatal("failed update replaced existing worker or metadata")
			}
			if stages, _ := filepath.Glob(filepath.Join(filepath.Dir(f.path), ".worker-*")); len(stages) != 0 {
				t.Fatalf("temporary downloads leaked: %v", stages)
			}
			f.prepare("v0.1.0") // Failed updates must release the lock.
		})
	}
}

func TestWorkerReleaseRepairsCorruptionAndArchitectureChanges(t *testing.T) {
	for _, problem := range []string{"binary", "manifest", "architecture"} {
		t.Run(problem, func(t *testing.T) {
			f := newWorkerReleaseTest(t)
			f.installed("v2.0.0", "amd64", "old worker")
			f.release("v2.0.0", true, false, "2026-11-04T22:31:51Z")
			switch problem {
			case "binary":
				if err := os.WriteFile(f.path, []byte("corrupted"), 0700); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				if err := os.WriteFile(f.path+".release.json", []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "architecture":
				f.source.arch = "arm64"
			}
			if got := f.prepare("v2.0.0"); !strings.HasPrefix(got, "v2.0.0/lingosbot-worker-linux-") {
				t.Fatal("cache was not repaired:", got)
			}
			if len(f.paths()) != 3 {
				t.Fatalf("repair requests = %v", f.paths())
			}
		})
	}
}

func TestWorkerReleaseLockHeldUntilServerStops(t *testing.T) {
	f := newWorkerReleaseTest(t)
	f.installed("v2.0.0", "amd64", "cached worker")
	_, lock, err := f.source.prepare(context.Background(), f.path, f.seed, "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, second, err := f.source.prepare(context.Background(), f.path, f.seed, "v2.0.0"); err == nil || second != nil {
		t.Fatalf("concurrent server accepted: %v, %v", second, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	f.prepare("v2.0.0")
}

func TestWorkerReleaseExplicitBinaryBypassesManagement(t *testing.T) {
	t.Setenv("WORKER_BIN", "/fake/standalone-worker")
	t.Setenv("RELEASE_VERSION", "invalid")
	path, lock, err := prepareWorker(context.Background())
	if err != nil || path != "/fake/standalone-worker" || lock != nil {
		t.Fatalf("WORKER_BIN override = %q, %v, %v", path, lock, err)
	}
}

func TestWorkerReleaseInvalidInputsNeverUseNetwork(t *testing.T) {
	f := newWorkerReleaseTest(t)
	for _, version := range []string{"v1.0.0/../../webui.db", "latest-beta", ""} {
		if _, lock, err := f.source.prepare(context.Background(), f.path, f.seed, version); err == nil || lock != nil {
			t.Fatalf("accepted invalid version %q", version)
		}
	}
	f.source.arch = "386"
	if _, _, err := f.source.prepare(context.Background(), f.path, f.seed, "v1.0.0"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
	if len(f.paths()) != 0 {
		t.Fatal(f.paths())
	}
}

func TestWorkerReleaseStartupFailureLeavesDatabaseUntouched(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "webui.db")
	before := []byte("existing database, never opened on failed worker preparation")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_PATH", path)
	t.Setenv("ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("WORKER_BIN", "")
	t.Setenv("RELEASE_VERSION", "invalid")
	if err := serve(); err == nil || !strings.Contains(err.Error(), "RELEASE_VERSION") {
		t.Fatalf("startup error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("startup modified the database: %v", err)
	}
}

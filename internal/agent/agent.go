package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	Name               = "Initra Agent"
	ServiceName        = "InitraAgent"
	LinuxServiceName   = "initra-agent"
	defaultReleaseBase = "https://git.justw.tf/LightZirconite/setup-win/raw/branch/main"
	defaultCheckEvery  = 24 * time.Hour
	heartbeatEvery     = 15 * time.Minute
)

type Options struct {
	BaseURL string
	Version string
}

type manifestResponse struct {
	Version   string            `json:"version"`
	Artifacts map[string]string `json:"artifacts"`
	Sha256    map[string]string `json:"sha256"`
}

type Artifact struct {
	Key    string
	URL    string
	SHA256 string
}

func Main(args []string, version string) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Printf("initra-agent %s\n", printableVersion(version))
		return nil
	}
	if len(args) == 0 {
		return DefaultAction(Options{BaseURL: defaultBaseURL(""), Version: printableVersion(version)})
	}

	switch args[0] {
	case "run-service":
		opts, err := parseOptions(args[1:], version)
		if err != nil {
			return err
		}
		return RunService(context.Background(), opts)
	case "install-service":
		opts, err := parseOptions(args[1:], version)
		if err != nil {
			return err
		}
		return InstallService(opts)
	case "status":
		return PrintStatus()
	case "uninstall-service":
		return UninstallService()
	case "--apply-update":
		return applyStagedUpdate(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseOptions(args []string, version string) (Options, error) {
	var opts Options
	opts.Version = printableVersion(version)
	fs := flag.NewFlagSet("initra-agent", flag.ContinueOnError)
	fs.StringVar(&opts.BaseURL, "base-url", "", "release base URL")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.BaseURL = defaultBaseURL(opts.BaseURL)
	return opts, nil
}

func printableVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return "dev"
	}
	return strings.TrimSpace(version)
}

func defaultBaseURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return defaultReleaseBase
	}
	return strings.TrimRight(value, "/")
}

func runAgentLoop(ctx context.Context, opts Options) error {
	paths, err := agentPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.DataDir, 0o755); err != nil {
		return err
	}
	logger, closeLog, err := openAgentLogger(paths.LogPath)
	if err != nil {
		return err
	}
	defer closeLog()

	logger.Printf("%s started version=%s base_url=%s os=%s arch=%s", Name, opts.Version, opts.BaseURL, runtime.GOOS, runtime.GOARCH)
	if err := checkForUpdate(ctx, opts, paths, logger); err != nil {
		logger.Printf("initial update check failed: %v", err)
	}

	checkTicker := time.NewTicker(defaultCheckEvery)
	heartbeatTicker := time.NewTicker(heartbeatEvery)
	defer checkTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Printf("%s stopping", Name)
			return nil
		case <-heartbeatTicker.C:
			logger.Printf("heartbeat version=%s", opts.Version)
		case <-checkTicker.C:
			if err := checkForUpdate(ctx, opts, paths, logger); err != nil {
				logger.Printf("update check failed: %v", err)
			}
		}
	}
}

func openAgentLogger(path string) (*log.Logger, func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, func() {}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, err
	}
	writer := io.MultiWriter(file, os.Stdout)
	return log.New(writer, "", log.LstdFlags|log.LUTC), func() { _ = file.Close() }, nil
}

func checkForUpdate(ctx context.Context, opts Options, paths platformPaths, logger *log.Logger) error {
	manifest, err := fetchManifest(ctx, opts.BaseURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Version) == "" || manifest.Version == opts.Version || opts.Version == "dev" {
		return nil
	}
	artifact, ok := SelectAgentArtifact(manifest, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("manifest has no agent artifact for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	target := currentExecutablePath()
	if target == "" {
		return errors.New("could not resolve current agent binary path")
	}
	staged := filepath.Join(paths.DataDir, filepath.Base(target)+".new")
	if err := downloadFile(ctx, artifact.URL, staged); err != nil {
		return err
	}
	if artifact.SHA256 != "" {
		got, err := sha256File(staged)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, artifact.SHA256) {
			_ = os.Remove(staged)
			return fmt.Errorf("sha256 mismatch for staged agent update")
		}
	}
	logger.Printf("staged agent update version=%s path=%s", manifest.Version, staged)
	return StageUpdate(staged, target, logger)
}

func fetchManifest(ctx context.Context, baseURL string) (manifestResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/releases/latest.json", nil)
	if err != nil {
		return manifestResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return manifestResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return manifestResponse{}, fmt.Errorf("manifest returned %s", resp.Status)
	}
	var manifest manifestResponse
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return manifestResponse{}, err
	}
	return manifest, nil
}

func SelectAgentArtifact(manifest manifestResponse, goos, goarch string) (Artifact, bool) {
	key := "agent-" + goos + "-" + goarch
	if goos == "windows" {
		key = "agent-windows-" + goarch
	}
	url := strings.TrimSpace(manifest.Artifacts[key])
	if url == "" {
		return Artifact{}, false
	}
	return Artifact{Key: key, URL: url, SHA256: strings.TrimSpace(manifest.Sha256[key])}, true
}

func downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %s returned %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func currentExecutablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func copyFile(source, target string, mode os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func waitForEnter() {
	fmt.Println()
	fmt.Print("Press Enter to close this window...")
	_, _ = fmt.Scanln()
}

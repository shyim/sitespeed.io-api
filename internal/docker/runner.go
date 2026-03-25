package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/shyim/sitespeed-api/internal/models"
	"github.com/shyim/sitespeed-api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const labelApp = "sitespeed-api"
const labelID = "sitespeed-api-id"
const containerOutputDir = "/sitespeed.io"

type Runner struct {
	client        *client.Client
	image         string
	resultBaseDir string
	timeout       time.Duration
	networkName   string
	maxConcurrent chan struct{}
}

func NewRunner() (*Runner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping docker daemon: %w", err)
	}

	img := os.Getenv("SITESPEED_IMAGE")
	if img == "" {
		img = "sitespeedio/sitespeed.io:latest"
	}

	baseDir := os.Getenv("RESULT_BASE_DIR")
	if baseDir == "" {
		baseDir = "/tmp/sitespeed-results"
	}

	timeout := 300 * time.Second
	if v := os.Getenv("DOCKER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	maxConcurrent := 5
	if v := os.Getenv("MAX_CONCURRENT_ANALYSES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrent = n
		}
	}

	r := &Runner{
		client:        cli,
		image:         img,
		resultBaseDir: baseDir,
		timeout:       timeout,
		networkName:   os.Getenv("DOCKER_NETWORK"),
		maxConcurrent: make(chan struct{}, maxConcurrent),
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create result base dir: %w", err)
	}

	return r, nil
}

func (r *Runner) EnsureImage(ctx context.Context) error {
	ctx, span := observability.Tracer("docker-runner").Start(ctx, "docker.EnsureImage")
	defer span.End()
	span.SetAttributes(attribute.String("docker.image", r.image))

	_, err := r.client.ImageInspect(ctx, r.image)
	if err == nil {
		return nil
	}

	observability.Printf(ctx, "Pulling image %s", r.image)
	reader, err := r.client.ImagePull(ctx, r.image, image.PullOptions{})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to pull image")
		return fmt.Errorf("failed to pull image %s: %w", r.image, err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to read image pull response")
		return fmt.Errorf("failed to read image pull response: %w", err)
	}
	observability.Printf(ctx, "Image %s pulled", r.image)
	return nil
}

func (r *Runner) RunAnalysis(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
	ctx, span := observability.Tracer("docker-runner").Start(ctx, "docker.RunAnalysis")
	defer span.End()
	span.SetAttributes(
		attribute.String("analysis.id", id),
		attribute.String("docker.image", r.image),
		attribute.Int("analysis.url_count", len(req.URLs)),
	)

	select {
	case r.maxConcurrent <- struct{}{}:
		defer func() { <-r.maxConcurrent }()
	case <-ctx.Done():
		span.SetStatus(codes.Error, "timed out waiting for analysis slot")
		return "", fmt.Errorf("timed out waiting for available analysis slot")
	}

	args := []string{
		"--outputFolder", containerOutputDir,
		"--plugins.add", "analysisstorer",
		"--visualMetrics",
		"--video",
		"--viewPort", "1920x1080",
		"--browsertime.chrome.cleanUserDataDir=true",
		"--browsertime.iterations", "1",
	}
	for key, value := range req.Headers {
		args = append(args, "--browsertime.requestheader", fmt.Sprintf("%s:%s", key, value))
	}
	args = append(args, req.URLs...)

	containerCfg := &container.Config{
		Image: r.image,
		Cmd:   args,
		Labels: map[string]string{
			labelApp: "true",
			labelID:  id,
		},
	}

	hostCfg := &container.HostConfig{
		ShmSize: 2 * 1024 * 1024 * 1024,
		Resources: container.Resources{
			Memory:   4 * 1024 * 1024 * 1024,
			NanoCPUs: 2 * 1e9,
		},
	}

	var networkCfg *network.NetworkingConfig
	if r.networkName != "" {
		networkCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				r.networkName: {},
			},
		}
	}

	containerName := fmt.Sprintf("sitespeed-%s", id)

	resp, err := r.client.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, containerName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create container")
		return "", fmt.Errorf("failed to create container: %w", err)
	}
	containerID := resp.ID
	span.SetAttributes(attribute.String("docker.container_id", containerID))

	removeContainer := func() {
		removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.client.ContainerRemove(removeCtx, containerID, container.RemoveOptions{Force: true}); err != nil {
			observability.Errorf(ctx, "Failed to remove container %s: %v", containerID, err)
		}
	}

	if err := r.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to start container")
		removeContainer()
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	waitCh, errCh := r.client.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)
	select {
	case result := <-waitCh:
		if result.StatusCode != 0 {
			span.SetStatus(codes.Error, "sitespeed container failed")
			logs := r.getContainerLogs(ctx, containerID)
			removeContainer()
			return "", fmt.Errorf("sitespeed container exited with code %d: %s", result.StatusCode, logs)
		}
	case err := <-errCh:
		span.RecordError(err)
		span.SetStatus(codes.Error, "error waiting for container")
		stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stopCancel()
		if err := r.client.ContainerStop(stopCtx, containerID, container.StopOptions{}); err != nil {
			observability.Errorf(ctx, "Failed to stop container %s: %v", containerID, err)
		}
		removeContainer()
		return "", fmt.Errorf("error waiting for container: %w", err)
	}

	// Copy results from the stopped container
	localResultDir := filepath.Join(r.resultBaseDir, id)
	if err := os.RemoveAll(localResultDir); err != nil {
		observability.Errorf(ctx, "Failed to clean result dir: %v", err)
	}
	if err := os.MkdirAll(localResultDir, 0755); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create result dir")
		removeContainer()
		return "", fmt.Errorf("failed to create result dir: %w", err)
	}

	if err := r.copyFromContainer(ctx, containerID, containerOutputDir, localResultDir); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to copy container results")
		removeContainer()
		return "", fmt.Errorf("failed to copy results from container: %w", err)
	}

	removeContainer()
	return localResultDir, nil
}

func (r *Runner) copyFromContainer(ctx context.Context, containerID, srcPath, destDir string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()

	reader, _, err := r.client.CopyFromContainer(ctx, containerID, srcPath)
	if err != nil {
		return fmt.Errorf("CopyFromContainer failed: %w", err)
	}
	defer func() { _ = reader.Close() }()

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// The tar archive contains paths relative to the copied directory.
		// CopyFromContainer for "/sitespeed.io" returns entries like "sitespeed.io/...",
		// so strip the first path component.
		name := header.Name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		} else {
			// This is the root directory entry itself, skip it
			continue
		}
		if name == "" {
			continue
		}

		target := filepath.Join(destDir, filepath.FromSlash(name))

		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir failed: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir failed: %w", err)
			}
			f, err := os.Create(target)
			if err != nil {
				return fmt.Errorf("create file failed: %w", err)
			}
			defer func() { _ = f.Close() }()
			if _, err := io.Copy(f, tr); err != nil {
				return fmt.Errorf("write file failed: %w", err)
			}
		}
	}

	return nil
}

func (r *Runner) getContainerLogs(ctx context.Context, containerID string) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	reader, err := r.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "50",
	})
	if err != nil {
		return fmt.Sprintf("(failed to get logs: %v)", err)
	}
	defer func() { _ = reader.Close() }()

	out, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Sprintf("(failed to read logs: %v)", err)
	}
	return string(out)
}

func (r *Runner) CleanupOrphaned(ctx context.Context) error {
	f := filters.NewArgs()
	f.Add("label", labelApp+"=true")

	containers, err := r.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	threshold := time.Now().Add(-2 * r.timeout)
	for _, c := range containers {
		created := time.Unix(c.Created, 0)
		if created.Before(threshold) {
			observability.Printf(ctx, "Cleaning up orphaned container %s (created %s)", c.ID[:12], created.Format(time.RFC3339))
			if err := r.client.ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				observability.Errorf(ctx, "Failed to stop orphaned container %s: %v", c.ID[:12], err)
			}
			if err := r.client.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				observability.Errorf(ctx, "Failed to remove orphaned container %s: %v", c.ID[:12], err)
			}
		}
	}

	return nil
}

func (r *Runner) CleanupStaleResultDirs(maxAgeMinutes int) {
	entries, err := os.ReadDir(r.resultBaseDir)
	if err != nil {
		observability.Errorf(context.Background(), "Failed to read result base dir: %v", err)
		return
	}

	maxAge := time.Duration(maxAgeMinutes) * time.Minute
	now := time.Now()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			fullPath := filepath.Join(r.resultBaseDir, entry.Name())
			if err := os.RemoveAll(fullPath); err != nil {
				observability.Errorf(context.Background(), "Failed to clean up stale result dir %s: %v", fullPath, err)
			} else {
				observability.Printf(context.Background(), "Cleaned up stale result dir (%dmin old): %s", int(now.Sub(info.ModTime()).Minutes()), fullPath)
			}
		}
	}
}

func (r *Runner) Close() error {
	return r.client.Close()
}

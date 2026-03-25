package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/shyim/sitespeed-api/internal/models"
	"github.com/shyim/sitespeed-api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const labelApp = "sitespeed-api"
const labelID = "sitespeed-api-id"
const containerOutputDir = "/sitespeed.io"

type Runner struct {
	clientset     *kubernetes.Clientset
	restConfig    *rest.Config
	image         string
	namespace     string
	resultBaseDir string
	timeout       time.Duration
	maxConcurrent chan struct{}
}

func NewRunner() (*Runner, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig for local development
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	img := os.Getenv("SITESPEED_IMAGE")
	if img == "" {
		img = "sitespeedio/sitespeed.io:latest"
	}

	namespace := os.Getenv("K8S_NAMESPACE")
	if namespace == "" {
		// Try to read from the in-cluster namespace file
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			namespace = strings.TrimSpace(string(data))
		} else {
			namespace = "default"
		}
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
		clientset:     clientset,
		restConfig:    config,
		image:         img,
		namespace:     namespace,
		resultBaseDir: baseDir,
		timeout:       timeout,
		maxConcurrent: make(chan struct{}, maxConcurrent),
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create result base dir: %w", err)
	}

	return r, nil
}

func (r *Runner) RunAnalysis(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
	ctx, span := observability.Tracer("kubernetes-runner").Start(ctx, "kubernetes.RunAnalysis")
	defer span.End()
	span.SetAttributes(
		attribute.String("analysis.id", id),
		attribute.String("k8s.namespace", r.namespace),
		attribute.String("k8s.image", r.image),
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

	podName := fmt.Sprintf("sitespeed-%s", id)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: r.namespace,
			Labels: map[string]string{
				labelApp: "true",
				labelID:  id,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "sitespeed",
					Image: r.image,
					Args:  args,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
							corev1.ResourceCPU:    resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("4Gi"),
							corev1.ResourceCPU:    resource.MustParse("2"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "dshm",
							MountPath: "/dev/shm",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "dshm",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    corev1.StorageMediumMemory,
							SizeLimit: resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
						},
					},
				},
			},
		},
	}

	podsClient := r.clientset.CoreV1().Pods(r.namespace)

	// Clean up any existing pod with same name
	if err := podsClient.Delete(ctx, podName, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		observability.Errorf(ctx, "Failed to delete existing pod %s: %v", podName, err)
	}

	createdPod, err := podsClient.Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create pod")
		return "", fmt.Errorf("failed to create pod: %w", err)
	}
	span.SetAttributes(attribute.String("k8s.pod_name", createdPod.Name))

	deletePod := func() {
		deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := podsClient.Delete(deleteCtx, podName, metav1.DeleteOptions{}); err != nil {
			observability.Errorf(ctx, "Failed to delete pod %s: %v", podName, err)
		}
	}

	// Watch for pod completion
	timeoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	phase, err := r.waitForPodCompletion(timeoutCtx, podName, createdPod.ResourceVersion)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "error waiting for pod")
		logs := r.getPodLogs(ctx, podName)
		deletePod()
		return "", fmt.Errorf("error waiting for pod: %w\nLogs: %s", err, logs)
	}

	if phase != corev1.PodSucceeded {
		span.SetStatus(codes.Error, "sitespeed pod failed")
		logs := r.getPodLogs(ctx, podName)
		deletePod()
		return "", fmt.Errorf("sitespeed pod failed (phase: %s): %s", phase, logs)
	}

	// Copy results from pod
	localResultDir := filepath.Join(r.resultBaseDir, id)
	if err := os.RemoveAll(localResultDir); err != nil {
		observability.Errorf(ctx, "Failed to clean result dir: %v", err)
	}
	if err := os.MkdirAll(localResultDir, 0755); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create result dir")
		deletePod()
		return "", fmt.Errorf("failed to create result dir: %w", err)
	}

	if err := r.copyFromPod(ctx, podName, "sitespeed", containerOutputDir, localResultDir); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to copy pod results")
		deletePod()
		return "", fmt.Errorf("failed to copy results from pod: %w", err)
	}

	deletePod()
	return localResultDir, nil
}

func (r *Runner) waitForPodCompletion(ctx context.Context, podName, resourceVersion string) (corev1.PodPhase, error) {
	watcher, err := r.clientset.CoreV1().Pods(r.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector:   fmt.Sprintf("metadata.name=%s", podName),
		ResourceVersion: resourceVersion,
	})
	if err != nil {
		return "", fmt.Errorf("failed to watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Modified:
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			switch pod.Status.Phase {
			case corev1.PodSucceeded, corev1.PodFailed:
				return pod.Status.Phase, nil
			}
		case watch.Deleted:
			return "", fmt.Errorf("pod was deleted unexpectedly")
		case watch.Error:
			return "", fmt.Errorf("watch error: %v", event.Object)
		}
	}

	return "", fmt.Errorf("watch channel closed (likely timeout)")
}

func (r *Runner) copyFromPod(ctx context.Context, podName, containerName, srcPath, destDir string) error {
	req := r.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(r.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   []string{"tar", "cf", "-", "-C", srcPath, "."},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(r.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("exec failed: %w (stderr: %s)", err, stderr.String())
	}

	return extractTar(&stdout, destDir)
}

func extractTar(reader io.Reader, destDir string) error {
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		name := header.Name
		if name == "" || name == "." {
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
			if err := extractFile(target, tr); err != nil {
				return err
			}
		}
	}

	return nil
}

func extractFile(target string, r io.Reader) error {
	f, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create file failed: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write file failed: %w", err)
	}
	return nil
}

func (r *Runner) getPodLogs(ctx context.Context, podName string) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	tailLines := int64(50)
	req := r.clientset.CoreV1().Pods(r.namespace).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &tailLines,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Sprintf("(failed to get logs: %v)", err)
	}
	defer func() { _ = stream.Close() }()

	out, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Sprintf("(failed to read logs: %v)", err)
	}
	return string(out)
}

func (r *Runner) CleanupOrphaned(ctx context.Context) error {
	pods, err := r.clientset.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelApp + "=true",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	threshold := time.Now().Add(-2 * r.timeout)
	for _, pod := range pods.Items {
		if pod.CreationTimestamp.Time.Before(threshold) {
			observability.Printf(ctx, "Cleaning up orphaned pod %s (created %s)", pod.Name, pod.CreationTimestamp.Format(time.RFC3339))
			if err := r.clientset.CoreV1().Pods(r.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
				observability.Errorf(ctx, "Failed to delete orphaned pod %s: %v", pod.Name, err)
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
	return nil
}

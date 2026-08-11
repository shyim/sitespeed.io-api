package kubernetes

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// stubPods is a minimal podAPI for delete/create race tests.
type stubPods struct {
	mu sync.Mutex

	// exists reports whether Get should see the pod. When terminating is true,
	// Create fails with AlreadyExists ("object is being deleted").
	exists      bool
	terminating bool

	getCalls    int
	createCalls int
	deleteCalls int

	// afterGetsGone makes the pod disappear after this many Get calls (0 = never
	// auto-clear). Used to simulate asynchronous termination.
	afterGetsGone int

	// createFailTimes makes the first N Create calls return AlreadyExists even
	// when the pod is gone, then succeed (extra race window).
	createFailTimes int

	createErr error // non-conflict create error, if set
}

func (s *stubPods) Get(_ context.Context, name string, _ metav1.GetOptions) (*corev1.Pod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.afterGetsGone > 0 && s.getCalls >= s.afterGetsGone {
		s.exists = false
		s.terminating = false
	}
	if !s.exists {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}}, nil
}

func (s *stubPods) Delete(_ context.Context, _ string, _ metav1.DeleteOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	if !s.exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "pod")
	}
	// Mark terminating; stay visible until afterGetsGone / test clears it.
	s.terminating = true
	if s.afterGetsGone == 0 {
		// Immediate deletion when no delayed termination is configured.
		s.exists = false
		s.terminating = false
	}
	return nil
}

func (s *stubPods) Create(_ context.Context, pod *corev1.Pod, _ metav1.CreateOptions) (*corev1.Pod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.exists || s.terminating {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, pod.Name)
	}
	if s.createFailTimes > 0 {
		s.createFailTimes--
		// Mimic apiserver: name still reserved briefly after NotFound from Get.
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, pod.Name)
	}
	s.exists = true
	return pod.DeepCopy(), nil
}

func TestParseNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace only", raw: "   ", want: nil},
		{name: "single pair", raw: "disktype=ssd", want: map[string]string{"disktype": "ssd"}},
		{
			name: "multiple pairs",
			raw:  "disktype=ssd,pool=ci",
			want: map[string]string{"disktype": "ssd", "pool": "ci"},
		},
		{
			name: "trims whitespace around pairs",
			raw:  " disktype = ssd , pool = ci ",
			want: map[string]string{"disktype": "ssd", "pool": "ci"},
		},
		{
			name: "skips entries without a key",
			raw:  "=ssd,pool=ci",
			want: map[string]string{"pool": "ci"},
		},
		{
			name: "skips entries without an equals sign",
			raw:  "broken,pool=ci",
			want: map[string]string{"pool": "ci"},
		},
		{name: "empty value is allowed", raw: "drain=", want: map[string]string{"drain": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNodeSelector(tt.raw)
			if !maps.Equal(got, tt.want) {
				t.Errorf("parseNodeSelector(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestPodReady(t *testing.T) {
	initOK := corev1.ContainerStatus{
		Name:  "sitespeed",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
	}
	resultsReady := corev1.ContainerStatus{Name: "results", Ready: true}

	tests := []struct {
		name      string
		pod       *corev1.Pod
		wantReady bool
		wantErr   bool
	}{
		{
			name: "init running, not ready yet",
			pod:  &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}},
		},
		{
			name: "init done, sidecar not ready yet",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{initOK},
				ContainerStatuses:     []corev1.ContainerStatus{{Name: "results", Ready: false}},
			}},
		},
		{
			name: "ready",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{initOK},
				ContainerStatuses:     []corev1.ContainerStatus{resultsReady},
			}},
			wantReady: true,
		},
		{
			name: "init container failed",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name:  "sitespeed",
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
				}},
			}},
			wantErr: true,
		},
		{
			name:    "pod failed phase",
			pod:     &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, err := podReady(tt.pod)
			if (err != nil) != tt.wantErr {
				t.Fatalf("podReady() err = %v, wantErr %v", err, tt.wantErr)
			}
			if ready != tt.wantReady {
				t.Errorf("podReady() ready = %v, want %v", ready, tt.wantReady)
			}
		})
	}
}

func TestWaitUntilPodDeleted(t *testing.T) {
	prevInterval := podDeletionPollInterval
	podDeletionPollInterval = time.Millisecond
	t.Cleanup(func() { podDeletionPollInterval = prevInterval })

	t.Run("already gone", func(t *testing.T) {
		pods := &stubPods{exists: false}
		if err := waitUntilPodDeleted(context.Background(), pods, "sitespeed-abc"); err != nil {
			t.Fatalf("waitUntilPodDeleted() = %v", err)
		}
		if pods.getCalls < 1 {
			t.Fatalf("expected at least one Get, got %d", pods.getCalls)
		}
	})

	t.Run("waits until terminating pod is gone", func(t *testing.T) {
		pods := &stubPods{exists: true, terminating: true, afterGetsGone: 3}
		if err := waitUntilPodDeleted(context.Background(), pods, "sitespeed-abc"); err != nil {
			t.Fatalf("waitUntilPodDeleted() = %v", err)
		}
		if pods.getCalls < 3 {
			t.Fatalf("expected Get to poll until gone, got %d calls", pods.getCalls)
		}
		if pods.exists {
			t.Fatal("expected pod to be gone")
		}
	})

	t.Run("times out while still present", func(t *testing.T) {
		pods := &stubPods{exists: true, terminating: true}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := waitUntilPodDeleted(ctx, pods, "sitespeed-abc")
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected DeadlineExceeded, got %v", err)
		}
	})
}

func TestCreatePodWithRetry(t *testing.T) {
	prevInterval := podDeletionPollInterval
	prevAttempts := podCreateMaxAttempts
	podDeletionPollInterval = time.Millisecond
	podCreateMaxAttempts = 5
	t.Cleanup(func() {
		podDeletionPollInterval = prevInterval
		podCreateMaxAttempts = prevAttempts
	})

	newPod := func() *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sitespeed-abc", Namespace: "default"}}
	}

	t.Run("succeeds immediately when name is free", func(t *testing.T) {
		pods := &stubPods{}
		got, err := createPodWithRetry(context.Background(), pods, newPod())
		if err != nil {
			t.Fatalf("createPodWithRetry() = %v", err)
		}
		if got.Name != "sitespeed-abc" {
			t.Fatalf("got name %q", got.Name)
		}
		if pods.createCalls != 1 {
			t.Fatalf("createCalls = %d, want 1", pods.createCalls)
		}
	})

	t.Run("retries while object is being deleted", func(t *testing.T) {
		pods := &stubPods{
			exists:          true,
			terminating:     true,
			afterGetsGone:   2,
			createFailTimes: 1, // one AlreadyExists after Get reports NotFound
		}
		got, err := createPodWithRetry(context.Background(), pods, newPod())
		if err != nil {
			t.Fatalf("createPodWithRetry() = %v", err)
		}
		if got.Name != "sitespeed-abc" {
			t.Fatalf("got name %q", got.Name)
		}
		if pods.createCalls < 2 {
			t.Fatalf("expected retries, createCalls = %d", pods.createCalls)
		}
		if pods.deleteCalls < 1 {
			t.Fatalf("expected Delete on conflict, deleteCalls = %d", pods.deleteCalls)
		}
	})

	t.Run("non-conflict errors are not retried", func(t *testing.T) {
		pods := &stubPods{
			createErr: apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "sitespeed-abc", errors.New("denied")),
		}
		_, err := createPodWithRetry(context.Background(), pods, newPod())
		if err == nil {
			t.Fatal("expected error")
		}
		if !apierrors.IsForbidden(err) {
			t.Fatalf("expected forbidden, got %v", err)
		}
		if pods.createCalls != 1 {
			t.Fatalf("createCalls = %d, want 1", pods.createCalls)
		}
	})
}

func TestEnsurePodRecreated(t *testing.T) {
	prevInterval := podDeletionPollInterval
	podDeletionPollInterval = time.Millisecond
	t.Cleanup(func() { podDeletionPollInterval = prevInterval })

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sitespeed-shopmon-prod-environment-1", Namespace: "default"}}

	t.Run("deletes terminating pod then creates", func(t *testing.T) {
		pods := &stubPods{
			exists:        true,
			terminating:   true,
			afterGetsGone: 3,
		}
		got, err := ensurePodRecreated(context.Background(), pods, pod)
		if err != nil {
			t.Fatalf("ensurePodRecreated() = %v", err)
		}
		if got.Name != pod.Name {
			t.Fatalf("got name %q", got.Name)
		}
		if pods.deleteCalls < 1 {
			t.Fatalf("expected initial Delete, deleteCalls = %d", pods.deleteCalls)
		}
		if pods.createCalls < 1 {
			t.Fatalf("expected Create, createCalls = %d", pods.createCalls)
		}
		if !pods.exists {
			t.Fatal("expected created pod to exist")
		}
	})

	t.Run("creates when no prior pod", func(t *testing.T) {
		pods := &stubPods{}
		got, err := ensurePodRecreated(context.Background(), pods, pod)
		if err != nil {
			t.Fatalf("ensurePodRecreated() = %v", err)
		}
		if got.Name != pod.Name {
			t.Fatalf("got name %q", got.Name)
		}
		if pods.createCalls != 1 {
			t.Fatalf("createCalls = %d, want 1", pods.createCalls)
		}
	})
}

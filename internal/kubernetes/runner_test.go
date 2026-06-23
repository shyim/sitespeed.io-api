package kubernetes

import (
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

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

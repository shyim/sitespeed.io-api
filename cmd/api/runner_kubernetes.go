package main

import (
	"context"

	k8srunner "github.com/shyim/sitespeed-api/internal/kubernetes"
	"github.com/shyim/sitespeed-api/internal/runner"
)

func createKubernetesRunner(_ context.Context) (runner.Runner, error) {
	return k8srunner.NewRunner()
}

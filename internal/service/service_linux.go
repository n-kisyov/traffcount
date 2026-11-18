//go:build linux

package service

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// RunService executes runDaemon until SIGINT or SIGTERM is received (used by systemd Type=simple).
func RunService(runDaemon func(ctx context.Context) error) error {
	return defaultMgr.RunService(runDaemon)
}

// RunService executes runDaemon under systemd until SIGINT or SIGTERM is received.
func (m *Manager) RunService(runDaemon func(ctx context.Context) error) error {
	SetServiceProcessMode(true)
	defer SetServiceProcessMode(false)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runDaemon(ctx)
}

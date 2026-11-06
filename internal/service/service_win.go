//go:build windows

package service

import (
	"context"
	"time"

	"golang.org/x/sys/windows/svc"
)

type traffCountWinService struct {
	runDaemon func(ctx context.Context) error
}

func (m *traffCountWinService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- m.runDaemon(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case err := <-done:
			if err != nil {
				return false, 1
			}
			break loop
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				time.Sleep(100 * time.Millisecond)
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(8 * time.Second):
				}
				break loop
			}
		}
	}

	changes <- svc.Status{State: svc.Stopped}
	return false, 0
}

// RunService executes runDaemon under the Windows Service Control Manager.
func RunService(runDaemon func(ctx context.Context) error) error {
	return svc.Run(ServiceName, &traffCountWinService{runDaemon: runDaemon})
}


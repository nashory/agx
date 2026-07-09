//go:build windows

package runtime

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// windowsServiceName is the Windows Service Control Manager identifier for the
// AGX runtime service.
const windowsServiceName = "AGXRuntime"

// agxWindowsService adapts the runtime daemon to the SCM service handler.
type agxWindowsService struct {
	version string
}

func (s agxWindowsService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- NewService(s.version).Start(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-runErr
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
			}
		case err := <-runErr:
			// The runtime exited on its own; report a non-zero exit on error.
			changes <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}

// RunAsWindowsService runs the runtime under the Windows Service Control Manager.
// It blocks until the service is stopped.
func RunAsWindowsService(version string) error {
	return svc.Run(windowsServiceName, agxWindowsService{version: version})
}

//go:build windows

package runtime

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsWhisperJob owns a kill-on-close Job Object. Closing the final handle
// terminates the Whisper server even when AGX itself is killed before it can run
// normal shutdown cleanup.
type windowsWhisperJob struct {
	mu     sync.Mutex
	handle windows.Handle
}

func guardWhisperServerProcess(process *os.Process) (whisperProcessGuard, error) {
	if process == nil {
		return nil, fmt.Errorf("process is nil")
	}
	job, err := newWhisperKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	assignErr := windows.AssignProcessToJobObject(job, handle)
	_ = windows.CloseHandle(handle)
	if assignErr != nil {
		_ = windows.CloseHandle(job)
		return nil, assignErr
	}
	return &windowsWhisperJob{handle: job}, nil
}

func newWhisperKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func (j *windowsWhisperJob) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}

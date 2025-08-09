package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// Process represents a managed process
type Process interface {
	// PID returns the process ID
	PID() int
	
	// IsRunning checks if the process is still running
	IsRunning() bool
	
	// Stop gracefully stops the process
	Stop(ctx context.Context) error
	
	// Kill forcefully kills the process
	Kill() error
	
	// Wait waits for the process to exit
	Wait() error
	
	// ExitCode returns the exit code (after process exits)
	ExitCode() int
}

// ProcessRunner manages process lifecycle
type ProcessRunner interface {
	// Start starts a new process
	Start(config ProcessConfig) (Process, error)
	
	// FindByPID finds a process by PID
	FindByPID(pid int) (Process, error)
	
	// Cleanup cleans up any zombie processes
	Cleanup() error
}

// ProcessConfig contains process configuration
type ProcessConfig struct {
	Command  string                 `json:"command"`
	Args     []string               `json:"args,omitempty"`
	WorkDir  string                 `json:"work_dir"`
	Env      map[string]string      `json:"env,omitempty"`
	Instance *types.Instance        `json:"instance"`
	LogFile  string                 `json:"log_file,omitempty"`
}

// LocalProcess represents a local OS process
type LocalProcess struct {
	cmd      *exec.Cmd
	pid      int
	running  bool
	exitCode int
	mu       sync.RWMutex
}

// LocalProcessRunner runs processes on the local machine
type LocalProcessRunner struct {
	processes map[int]*LocalProcess
	mu        sync.RWMutex
}

// NewLocalProcessRunner creates a new local process runner
func NewLocalProcessRunner() *LocalProcessRunner {
	return &LocalProcessRunner{
		processes: make(map[int]*LocalProcess),
	}
}

// Start starts a new process
func (r *LocalProcessRunner) Start(config ProcessConfig) (Process, error) {
	// Parse command and args
	var cmd *exec.Cmd
	if len(config.Args) > 0 {
		cmd = exec.Command(config.Command, config.Args...)
	} else {
		// If no args provided, use shell to execute command
		cmd = exec.Command("sh", "-c", config.Command)
	}
	
	// Set working directory
	if config.WorkDir != "" {
		cmd.Dir = config.WorkDir
	}
	
	// Set environment variables
	cmd.Env = os.Environ()
	for k, v := range config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	
	// Set up logging
	if config.LogFile != "" {
		logFile, err := os.OpenFile(config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		// Log to /tmp for debugging
		logFile := fmt.Sprintf("/tmp/%s-%d.log", config.Instance.ServiceName, time.Now().Unix())
		if f, err := os.Create(logFile); err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
		}
	}
	
	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}
	
	process := &LocalProcess{
		cmd:     cmd,
		pid:     cmd.Process.Pid,
		running: true,
	}
	
	// Store process
	r.mu.Lock()
	r.processes[process.pid] = process
	r.mu.Unlock()
	
	// Monitor process
	go process.monitor()
	
	return process, nil
}

// FindByPID finds a process by PID
func (r *LocalProcessRunner) FindByPID(pid int) (Process, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	process, exists := r.processes[pid]
	if !exists {
		return nil, fmt.Errorf("process with PID %d not found", pid)
	}
	
	return process, nil
}

// Cleanup cleans up any zombie processes
func (r *LocalProcessRunner) Cleanup() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for pid, process := range r.processes {
		if !process.IsRunning() {
			delete(r.processes, pid)
		}
	}
	
	return nil
}

// PID returns the process ID
func (p *LocalProcess) PID() int {
	return p.pid
}

// IsRunning checks if the process is still running
func (p *LocalProcess) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// Stop gracefully stops the process
func (p *LocalProcess) Stop(ctx context.Context) error {
	if !p.IsRunning() {
		return nil
	}
	
	// Send SIGTERM for graceful shutdown
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}
	
	// Wait for process to exit or timeout
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	
	select {
	case <-ctx.Done():
		// Context cancelled, force kill
		return p.Kill()
	case err := <-done:
		p.mu.Lock()
		p.running = false
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				p.exitCode = exitErr.ExitCode()
			}
		}
		p.mu.Unlock()
		return nil
	}
}

// Kill forcefully kills the process
func (p *LocalProcess) Kill() error {
	if !p.IsRunning() {
		return nil
	}
	
	if err := p.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}
	
	p.mu.Lock()
	p.running = false
	p.exitCode = -1
	p.mu.Unlock()
	
	return nil
}

// Wait waits for the process to exit
func (p *LocalProcess) Wait() error {
	if !p.IsRunning() {
		return nil
	}
	
	err := p.cmd.Wait()
	
	p.mu.Lock()
	p.running = false
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			p.exitCode = exitErr.ExitCode()
		}
	}
	p.mu.Unlock()
	
	return err
}

// ExitCode returns the exit code
func (p *LocalProcess) ExitCode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exitCode
}

// monitor monitors the process status
func (p *LocalProcess) monitor() {
	p.cmd.Wait()
	
	p.mu.Lock()
	p.running = false
	if p.cmd.ProcessState != nil {
		p.exitCode = p.cmd.ProcessState.ExitCode()
	}
	p.mu.Unlock()
}

// MockProcess is a mock process for testing
type MockProcess struct {
	pid      int
	running  bool
	exitCode int
	mu       sync.RWMutex
}

// MockProcessRunner is a mock process runner for testing
type MockProcessRunner struct {
	processes map[int]*MockProcess
	nextPID   int
	mu        sync.RWMutex
}

// NewMockProcessRunner creates a new mock process runner
func NewMockProcessRunner() *MockProcessRunner {
	return &MockProcessRunner{
		processes: make(map[int]*MockProcess),
		nextPID:   1000,
	}
}

// Start starts a mock process
func (r *MockProcessRunner) Start(config ProcessConfig) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	pid := r.nextPID
	r.nextPID++
	
	process := &MockProcess{
		pid:     pid,
		running: true,
	}
	
	r.processes[pid] = process
	
	return process, nil
}

// FindByPID finds a mock process by PID
func (r *MockProcessRunner) FindByPID(pid int) (Process, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	process, exists := r.processes[pid]
	if !exists {
		return nil, fmt.Errorf("process with PID %d not found", pid)
	}
	
	return process, nil
}

// Cleanup cleans up mock processes
func (r *MockProcessRunner) Cleanup() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for pid, process := range r.processes {
		if !process.IsRunning() {
			delete(r.processes, pid)
		}
	}
	
	return nil
}

// PID returns the mock process ID
func (p *MockProcess) PID() int {
	return p.pid
}

// IsRunning checks if the mock process is running
func (p *MockProcess) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// Stop stops the mock process
func (p *MockProcess) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	p.exitCode = 0
	return nil
}

// Kill kills the mock process
func (p *MockProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	p.exitCode = -1
	return nil
}

// Wait waits for the mock process
func (p *MockProcess) Wait() error {
	for p.IsRunning() {
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// ExitCode returns the mock exit code
func (p *MockProcess) ExitCode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exitCode
}

// SetRunning sets the running state of the mock process
func (p *MockProcess) SetRunning(running bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = running
}
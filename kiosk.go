package viamkiosk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	generic "go.viam.com/rdk/services/generic"
)

var (
	Kiosk            = resource.NewModel("erh", "viam-kiosk", "Kiosk")
	errUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterService(generic.API, Kiosk,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newViamKioskKiosk,
		},
	)
}

type Config struct {
	URL            string `json:"url"`
	RefreshSeconds int    `json:"refresh_seconds"`
}

func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.URL == "" {
		return nil, nil, fmt.Errorf("need a url")
	}
	return nil, nil, nil
}

type viamKioskKiosk struct {
	resource.AlwaysRebuild

	name resource.Name

	logger logging.Logger
	cfg    *Config

	cancelCtx  context.Context
	cancelFunc func()

	mu            sync.Mutex
	cmd           *exec.Cmd
	xdgRuntimeDir string
}

func newViamKioskKiosk(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewKiosk(ctx, deps, rawConf.ResourceName(), conf, logger)

}

func NewKiosk(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	// Ensure XDG_RUNTIME_DIR exists for Wayland
	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		xdgRuntimeDir = "/run/user/0"
		os.MkdirAll(xdgRuntimeDir, 0700)
	}

	s := &viamKioskKiosk{
		name:          name,
		logger:        logger,
		cfg:           conf,
		cancelCtx:     cancelCtx,
		cancelFunc:    cancelFunc,
		xdgRuntimeDir: xdgRuntimeDir,
	}

	if err := s.startBrowser(); err != nil {
		cancelFunc()
		return nil, err
	}

	// Start refresh loop if configured
	if conf.RefreshSeconds > 0 {
		go s.refreshLoop()
	}

	return s, nil
}

func (s *viamKioskKiosk) startBrowser() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.Command("cage", "-s", "--", "chromium", "--kiosk", "--noerrdialogs", "--disable-infobars", "--no-first-run", "--no-sandbox", s.cfg.URL)
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+s.xdgRuntimeDir,
		"WLR_LIBINPUT_NO_DEVICES=1",
		"LIBSEAT_BACKEND=noop",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start kiosk: %w", err)
	}

	s.cmd = cmd

	// Log stdout as info
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			s.logger.Infof("kiosk: %s", scanner.Text())
		}
	}()

	// Log stderr as warning
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			s.logger.Warnf("kiosk: %s", scanner.Text())
		}
	}()

	return nil
}

func (s *viamKioskKiosk) stopBrowser() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
		s.cmd = nil
	}
}

func (s *viamKioskKiosk) refreshLoop() {
	ticker := time.NewTicker(time.Duration(s.cfg.RefreshSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.cancelCtx.Done():
			return
		case <-ticker.C:
			s.logger.Infof("refreshing kiosk")
			s.stopBrowser()
			if err := s.startBrowser(); err != nil {
				s.logger.Errorf("failed to restart browser: %v", err)
			}
		}
	}
}

func (s *viamKioskKiosk) Name() resource.Name {
	return s.name
}

func (s *viamKioskKiosk) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *viamKioskKiosk) Close(context.Context) error {
	s.cancelFunc()
	s.stopBrowser()
	return nil
}

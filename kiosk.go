package viamkiosk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

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

	cmd *exec.Cmd
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

	cmd := exec.Command("cage", "-s", "--", "chromium", "--kiosk", "--noerrdialogs", "--disable-infobars", "--no-first-run", "--no-sandbox", conf.URL)
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+xdgRuntimeDir,
		"WLR_LIBINPUT_NO_DEVICES=1",
		"LIBSEAT_BACKEND=noop",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelFunc()
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancelFunc()
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancelFunc()
		return nil, fmt.Errorf("failed to start kiosk: %w", err)
	}

	// Log stdout as info
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			logger.Infof("kiosk: %s", scanner.Text())
		}
	}()

	// Log stderr as warning
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logger.Warnf("kiosk: %s", scanner.Text())
		}
	}()

	s := &viamKioskKiosk{
		name:       name,
		logger:     logger,
		cfg:        conf,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
		cmd:        cmd,
	}
	return s, nil
}

func (s *viamKioskKiosk) Name() resource.Name {
	return s.name
}

func (s *viamKioskKiosk) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *viamKioskKiosk) Close(context.Context) error {
	s.cancelFunc()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	return nil
}

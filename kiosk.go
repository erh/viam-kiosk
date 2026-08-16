package viamkiosk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	// Scale enlarges everything by this factor, e.g. 2 renders the page
	// at twice the size, useful on high-dpi displays. 0 means default.
	Scale float64 `json:"scale"`
	// Resolution sets the display output mode via wlr-randr,
	// e.g. "1280x720" or "1920x1080@60". Empty means the display's
	// native/preferred mode.
	Resolution string `json:"resolution"`
}

func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.URL == "" {
		return nil, nil, fmt.Errorf("need a url")
	}
	if cfg.Resolution != "" {
		var w, h int
		if n, err := fmt.Sscanf(cfg.Resolution, "%dx%d", &w, &h); n != 2 || err != nil {
			return nil, nil, fmt.Errorf("resolution must look like \"1280x720\" or \"1920x1080@60\", got %q", cfg.Resolution)
		}
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

// findBrowser locates the chromium binary, which is named differently
// across distros.
func findBrowser() (string, error) {
	for _, name := range []string{"chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("chromium not found; run the module's first_run script or install chromium manually")
}

func (s *viamKioskKiosk) startBrowser() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := exec.LookPath("cage"); err != nil {
		return errors.New("cage not found; run the module's first_run script or install cage manually")
	}

	browser, err := findBrowser()
	if err != nil {
		return err
	}

	args := []string{"-s", "--", browser, "--kiosk", "--noerrdialogs", "--disable-infobars", "--no-first-run", "--no-sandbox"}
	if s.cfg.Scale > 0 {
		args = append(args, fmt.Sprintf("--force-device-scale-factor=%g", s.cfg.Scale))
	}
	args = append(args, s.cfg.URL)

	cmd := exec.Command("cage", args...)
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

	if s.cfg.Resolution != "" {
		go s.applyResolution()
	}

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

// findWaylandDisplay finds the wayland socket cage created in our runtime dir.
func (s *viamKioskKiosk) findWaylandDisplay() (string, error) {
	entries, err := os.ReadDir(s.xdgRuntimeDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "wayland-") && !strings.HasSuffix(name, ".lock") {
			return name, nil
		}
	}
	return "", errors.New("no wayland socket yet")
}

func (s *viamKioskKiosk) runWlrRandr(display string, args ...string) (string, error) {
	cmd := exec.Command("wlr-randr", args...)
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+s.xdgRuntimeDir,
		"WAYLAND_DISPLAY="+display,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// applyResolution sets the display mode via wlr-randr once cage's wayland
// socket is up. Retries for a bit because cage takes a moment to start.
func (s *viamKioskKiosk) applyResolution() {
	res := s.cfg.Resolution

	var lastErr string
	for i := 0; i < 30; i++ {
		select {
		case <-s.cancelCtx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}

		display, err := s.findWaylandDisplay()
		if err != nil {
			lastErr = err.Error()
			continue
		}

		// list outputs to get the first output's name
		listing, err := s.runWlrRandr(display)
		if err != nil {
			lastErr = fmt.Sprintf("wlr-randr: %v: %s", err, listing)
			continue
		}
		output := ""
		for _, line := range strings.Split(listing, "\n") {
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				output = strings.Fields(line)[0]
				break
			}
		}
		if output == "" {
			lastErr = "no outputs listed by wlr-randr"
			continue
		}

		if out, err := s.runWlrRandr(display, "--output", output, "--mode", res); err == nil {
			s.logger.Infof("set output %s to mode %s", output, res)
			return
		} else {
			lastErr = fmt.Sprintf("wlr-randr --mode: %v: %s", err, out)
		}
		// not an advertised mode, ask the display to do it anyway
		if out, err := s.runWlrRandr(display, "--output", output, "--custom-mode", res); err == nil {
			s.logger.Infof("set output %s to custom mode %s", output, res)
			return
		} else {
			lastErr = fmt.Sprintf("wlr-randr --custom-mode: %v: %s", err, out)
		}
	}
	s.logger.Errorf("failed to set resolution %q (is wlr-randr installed and cage >= 0.1.5?): %s", res, lastErr)
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

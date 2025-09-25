package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
)

// ctxKeyOptions is used to store options within a cobra command context.
type ctxKeyOptions struct{}

// Options contains global flags shared by all commands.
type Options struct {
	RootDir    string
	JSONOutput bool
	Verbose    bool
	DryRun     bool
	LogFile    string

	logger   *logrus.Logger
	logClose func() error
}

var (
	optionsMu sync.RWMutex
	current   *Options
)

// New creates a new Options instance populated with defaults.
func New() *Options {
	return &Options{}
}

// Init populates options and configures logging.
func (o *Options) Init(root string, jsonOut, verbose, dry bool, logFile string) error {
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to determine current directory: %w", err)
		}
		root = cwd
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("failed to resolve root path: %w", err)
	}

	if _, err := os.Stat(absRoot); err != nil {
		return fmt.Errorf("root path invalid: %w", err)
	}

	if filepath.Base(absRoot) != ".virtualboard" {
		workspace := filepath.Join(absRoot, ".virtualboard")
		if info, err := os.Stat(workspace); err == nil && info.IsDir() {
			absRoot = workspace
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect workspace: %w", err)
		}
	}

	featuresPath := filepath.Join(absRoot, "features")
	if _, err := os.Stat(featuresPath); errors.Is(err, os.ErrNotExist) {
		alt := filepath.Join(absRoot, "src")
		if info, altErr := os.Stat(alt); altErr == nil && info.IsDir() {
			if _, innerErr := os.Stat(filepath.Join(alt, "features")); innerErr == nil {
				absRoot = alt
			}
		}
	}

	o.RootDir = absRoot
	o.JSONOutput = jsonOut
	o.Verbose = verbose
	o.DryRun = dry
	o.LogFile = logFile

	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})

	if verbose {
		logger.SetLevel(logrus.InfoLevel)
		var output io.Writer = os.Stderr
		if logFile != "" {
			// #nosec G304 -- log file path provided via command flag
			f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return fmt.Errorf("failed to open log file: %w", err)
			}
			output = f
			o.logClose = f.Close
		}
		logger.SetOutput(output)
	} else {
		logger.SetLevel(logrus.WarnLevel)
		logger.SetOutput(io.Discard)
	}

	o.logger = logger
	SetCurrent(o)

	return nil
}

// SetCurrent stores the provided options as the globally accessible configuration.
func SetCurrent(o *Options) {
	optionsMu.Lock()
	defer optionsMu.Unlock()
	current = o
}

// Current retrieves the globally stored options.
func Current() (*Options, error) {
	optionsMu.RLock()
	defer optionsMu.RUnlock()
	if current == nil {
		return nil, fmt.Errorf("configuration not initialised")
	}
	return current, nil
}

// Close releases any resources held by options (e.g., log files).
func (o *Options) Close() error {
	if o.logClose != nil {
		return o.logClose()
	}
	return nil
}

// WithContext returns a new context with the options stored.
func (o *Options) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyOptions{}, o)
}

// FromContext extracts Options from command context.
func FromContext(ctx context.Context) (*Options, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context provided")
	}
	if opts, ok := ctx.Value(ctxKeyOptions{}).(*Options); ok {
		return opts, nil
	}
	return Current()
}

// Logger exposes the configured logger.
func (o *Options) Logger() *logrus.Logger {
	return o.logger
}

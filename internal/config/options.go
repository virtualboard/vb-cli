package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var actorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var (
	// ErrActorRequired indicates that a mutating operation omitted a stable actor.
	ErrActorRequired = errors.New("explicit actor identity is required")
	// ErrActorInvalid indicates that the supplied actor is unsafe or malformed.
	ErrActorInvalid = errors.New("invalid actor identity")
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
	Actor      string

	logger   *logrus.Logger
	logClose func() error
}

// EffectiveActor resolves the identity used for ownership and lock checks.
// An explicit --actor value wins, followed by agent-oriented environment
// variables. OS usernames are deliberately excluded because multiple agents
// commonly share one operating-system account.
func (o *Options) EffectiveActor() string {
	for _, actor := range []string{o.Actor, os.Getenv("VIRTUALBOARD_ACTOR"), os.Getenv("AGENT_ID")} {
		if actor = strings.TrimSpace(actor); actor != "" {
			return actor
		}
	}
	return ""
}

// RequireActor returns a valid explicit coordination identity for mutations.
func (o *Options) RequireActor() (string, error) {
	actor := o.EffectiveActor()
	if actor == "" {
		return "", fmt.Errorf("%w: pass --actor or set VIRTUALBOARD_ACTOR/AGENT_ID", ErrActorRequired)
	}
	if !actorPattern.MatchString(actor) || strings.EqualFold(actor, "unassigned") || strings.EqualFold(actor, "unknown") {
		return "", fmt.Errorf("%w %q", ErrActorInvalid, actor)
	}
	return actor, nil
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
		root = os.Getenv("VIRTUALBOARD_ROOT")
	}
	if strings.TrimSpace(root) == "" {
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

	// #nosec G703 -- absRoot is the caller-selected workspace root whose existence must be inspected.
	if _, err := os.Stat(absRoot); err != nil {
		return fmt.Errorf("root path invalid: %w", err)
	}

	if filepath.Base(absRoot) != ".virtualboard" {
		workspace := filepath.Join(absRoot, ".virtualboard")
		// #nosec G703 -- workspace is a fixed child of the caller-selected root.
		if info, err := os.Lstat(workspace); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symbolic-link workspace root %s", workspace)
			}
			if info.IsDir() {
				absRoot = workspace
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect workspace: %w", err)
		}
		// #nosec G703 -- absRoot is the caller-selected workspace root and is checked without following a leaf symlink.
	} else if info, err := os.Lstat(absRoot); err != nil {
		return fmt.Errorf("failed to inspect workspace root: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symbolic-link workspace root %s", absRoot)
	}

	featuresPath := filepath.Join(absRoot, "features")
	// #nosec G703 -- featuresPath is a fixed child used only for legacy-root discovery.
	if _, err := os.Stat(featuresPath); errors.Is(err, os.ErrNotExist) {
		alt := filepath.Join(absRoot, "src")
		// #nosec G703 -- alt is the fixed legacy src child and its leaf must be a real directory.
		if info, altErr := os.Lstat(alt); altErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			// #nosec G703 -- the inspected features path is a fixed child of the validated legacy src directory.
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

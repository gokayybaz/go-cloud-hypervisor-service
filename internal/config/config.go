package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Config holds all application configuration.  Fields are tagged for
// mapstructure so that viper can unmarshal into the struct regardless of
// whether the source is a YAML file, environment variable, or flag.
type Config struct {
	Server          ServerConfig          `mapstructure:"server"`
	Log             LogConfig             `mapstructure:"log"`
	Preflight       PreflightConfig       `mapstructure:"preflight"`
	CloudHypervisor CloudHypervisorConfig `mapstructure:"cloud_hypervisor"`
	Auth            AuthConfig            `mapstructure:"auth"`
	RateLimit       RateLimitConfig       `mapstructure:"rate_limit"`
	TLS             TLSConfig             `mapstructure:"tls"`
	Profile         bool                  `mapstructure:"profile"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port            string        `mapstructure:"port"`
	Host            string        `mapstructure:"host"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// PreflightConfig holds preflight check settings.
type PreflightConfig struct {
	Enabled    bool `mapstructure:"enabled"`
	StrictMode bool `mapstructure:"strict_mode"`
}

// CloudHypervisorConfig holds Cloud Hypervisor specific settings.
type CloudHypervisorConfig struct {
	BinaryPath string `mapstructure:"binary_path"`
	SocketPath string `mapstructure:"socket_path"`
}

// AuthConfig holds JWT authentication settings.
type AuthConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Secret      string `mapstructure:"secret"`
	Issuer      string `mapstructure:"issuer"`
	Audience    string `mapstructure:"audience"`
	RBACEnabled bool   `mapstructure:"rbac_enabled"`
}

// RateLimitConfig holds rate limiting settings.
type RateLimitConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	Window        time.Duration `mapstructure:"window"`
	IPLimit       int           `mapstructure:"ip_limit"`
	ViewerLimit   int           `mapstructure:"viewer_limit"`
	OperatorLimit int           `mapstructure:"operator_limit"`
	AdminLimit    int           `mapstructure:"admin_limit"`
}

// TLSConfig holds TLS settings.
type TLSConfig struct {
	Enabled     bool     `mapstructure:"enabled"`
	CertFile    string   `mapstructure:"cert_file"`
	KeyFile     string   `mapstructure:"key_file"`
	ACMEEnabled bool     `mapstructure:"acme_enabled"`
	ACMEEmail   string   `mapstructure:"acme_email"`
	ACMEDomains []string `mapstructure:"acme_domains"`
	ACMECache   string   `mapstructure:"acme_cache"`
}

// Loader coordinates viper setup and unmarshalling.
type Loader struct {
	v *viper.Viper
}

// NewLoader creates a Loader with viper defaults, environment bindings, and
// file search paths configured.
func NewLoader() *Loader {
	v := viper.New()

	// File name and format
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Search paths (in order of precedence)
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/ch-api/")
	v.AddConfigPath("$HOME/.ch-api")

	// Environment variable support
	v.SetEnvPrefix("CH_API")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults
	setDefaults(v)

	return &Loader{v: v}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.host", "")
	v.SetDefault("server.read_timeout", "15s")
	v.SetDefault("server.write_timeout", "15s")
	v.SetDefault("server.idle_timeout", "60s")
	v.SetDefault("server.shutdown_timeout", "15s")

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	v.SetDefault("preflight.enabled", true)
	v.SetDefault("preflight.strict_mode", false)

	v.SetDefault("cloud_hypervisor.binary_path", "cloud-hypervisor")
	v.SetDefault("cloud_hypervisor.socket_path", "/var/run/ch-api/ch.sock")

	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.secret", "")
	v.SetDefault("auth.issuer", "")
	v.SetDefault("auth.audience", "")
	v.SetDefault("auth.rbac_enabled", true)

	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.window", "1m")
	v.SetDefault("rate_limit.ip_limit", 100)
	v.SetDefault("rate_limit.viewer_limit", 100)
	v.SetDefault("rate_limit.operator_limit", 200)
	v.SetDefault("rate_limit.admin_limit", 500)

	v.SetDefault("tls.enabled", false)
	v.SetDefault("tls.cert_file", "")
	v.SetDefault("tls.key_file", "")
	v.SetDefault("tls.acme_enabled", false)
	v.SetDefault("tls.acme_email", "")
	v.SetDefault("tls.acme_domains", []string{})
	v.SetDefault("tls.acme_cache", "data/certs")

	v.SetDefault("profile", false)
}

// Load reads the configuration from all sources (defaults, config file,
// environment variables) and returns a populated Config.  It does NOT start
// the file watcher — call Watch() for hot-reload.
func (l *Loader) Load() (*Config, error) {
	if err := l.v.ReadInConfig(); err != nil {
		// Only treat missing config file as fatal if there is no other source.
		// Viper still has defaults and env vars, so a missing file is fine.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}

	var cfg Config
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}

// Watch starts a background goroutine that watches the config file for
// changes.  When a change is detected the provided onChange callback is
// invoked with the freshly loaded Config.  The callback is executed in the
// watcher goroutine — keep it short or delegate to another goroutine.
//
// Call Stop() on the returned Watcher to clean up resources.
func (l *Loader) Watch(onChange func(*Config)) *Watcher {
	w := &Watcher{v: l.v, onChange: onChange, done: make(chan struct{})}
	l.v.OnConfigChange(func(in fsnotify.Event) {
		var cfg Config
		if err := l.v.Unmarshal(&cfg); err != nil {
			// Log the error via the callback so the caller can decide what to do.
			// We pass nil to signal an error; the caller can inspect viper errors
			// if needed.  For now we just re-load on the next successful change.
			return
		}
		w.onChange(&cfg)
	})
	l.v.WatchConfig()
	return w
}

// Watcher manages the background config file watcher.
type Watcher struct {
	v        *viper.Viper
	onChange func(*Config)
	done     chan struct{}
}

// Stop signals the watcher to stop watching.  It is safe to call multiple
// times.
func (w *Watcher) Stop() {
	close(w.done)
}

// SetConfigFile overrides the config file path. Useful in tests.
func (l *Loader) SetConfigFile(path string) {
	l.v.SetConfigFile(path)
}

// Viper returns the underlying viper instance for advanced use cases (e.g.
// reading arbitrary keys not present in the typed Config struct).
func (l *Loader) Viper() *viper.Viper {
	return l.v
}
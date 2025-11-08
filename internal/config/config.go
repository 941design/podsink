package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"gopkg.in/yaml.v3"

	"podsink/internal/theme"
)

// Config represents the persisted application configuration.
type Config struct {
	DownloadRoot               string `yaml:"download_root"`
	ParallelDownloads          int    `yaml:"parallel_downloads"`
	TmpDir                     string `yaml:"tmp_dir"`
	RetryCount                 int    `yaml:"retry_count"`
	RetryBackoffMaxSec         int    `yaml:"retry_backoff_max_seconds"`
	UserAgent                  string `yaml:"user_agent"`
	Proxy                      string `yaml:"proxy,omitempty"`
	TLSVerify                  bool   `yaml:"tls_verify"`
	ColorTheme                 string `yaml:"color_theme"`
	MaxEpisodes                int    `yaml:"max_episodes"`
	MaxEpisodeDescriptionLines int    `yaml:"max_episode_description_lines"`
	PodcastNameMaxLength       int    `yaml:"podcast_name_max_length"`
	EpisodeNameMaxLength       int    `yaml:"episode_name_max_length"`
}

// Defaults returns the baseline configuration used on first run.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	downloadRoot := filepath.Join(home, "Podcasts")
	return Config{
		DownloadRoot:               downloadRoot,
		ParallelDownloads:          4,
		TmpDir:                     os.TempDir(),
		RetryCount:                 3,
		RetryBackoffMaxSec:         60,
		UserAgent:                  "podsink/dev",
		TLSVerify:                  true,
		ColorTheme:                 theme.Default,
		MaxEpisodes:                12,
		MaxEpisodeDescriptionLines: 12,
		PodcastNameMaxLength:       16,
		EpisodeNameMaxLength:       40,
	}
}

// Ensure loads configuration from the provided path, prompting the user to
// create one if it does not yet exist.
func Ensure(ctx context.Context, path string) (Config, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, err
	}

	cfg = Defaults()
	if err := bootstrap(ctx, &cfg); err != nil {
		return Config{}, err
	}

	if err := Save(path, cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Load reads configuration from disk.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if strings.TrimSpace(cfg.ColorTheme) == "" {
		cfg.ColorTheme = theme.Default
	}
	if cfg.MaxEpisodes <= 0 {
		cfg.MaxEpisodes = Defaults().MaxEpisodes
	}
	if cfg.MaxEpisodeDescriptionLines <= 0 {
		cfg.MaxEpisodeDescriptionLines = Defaults().MaxEpisodeDescriptionLines
	}
	return cfg, nil
}

// Save writes configuration back to disk, ensuring directory permissions are restrictive.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func bootstrap(ctx context.Context, cfg *Config) error {
	if fromEnv := strings.TrimSpace(os.Getenv("PODSINK_DOWNLOAD_ROOT")); fromEnv != "" {
		resolved, err := expandPath(fromEnv)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(resolved, 0o755); err != nil {
			return fmt.Errorf("create download directory: %w", err)
		}
		cfg.DownloadRoot = resolved
		return nil
	}

	prompt := &survey.Input{
		Message: "Choose a download directory",
		Default: cfg.DownloadRoot,
	}

	var answer string
	if err := survey.AskOne(prompt, &answer, survey.WithValidator(survey.Required)); err != nil {
		if errors.Is(err, terminal.InterruptErr) {
			return fmt.Errorf("initialisation interrupted")
		}
		return err
	}

	answer = strings.TrimSpace(answer)
	if answer == "" {
		return fmt.Errorf("download directory cannot be empty")
	}

	resolved, err := expandPath(answer)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	cfg.DownloadRoot = resolved
	return nil
}

// EditableKeys returns the ordered list of configuration keys exposed via the
// interactive editor.
func EditableKeys() []string {
	return []string{
		"download_root",
		"parallel_downloads",
		"tmp_dir",
		"retry_count",
		"retry_backoff_max_seconds",
		"user_agent",
		"proxy",
		"tls_verify",
		"color_theme",
		"max_episodes",
		"max_episode_description_lines",
	}
}

// EditInteractive opens an interactive survey session allowing the user to
// update configuration values.
func EditInteractive(ctx context.Context, cfg Config) (Config, error) {
	questions := []*survey.Question{
		{
			Name: "download_root",
			Prompt: &survey.Input{
				Message: "Download directory",
				Default: cfg.DownloadRoot,
			},
			Validate: survey.Required,
		},
		{
			Name: "parallel_downloads",
			Prompt: &survey.Input{
				Message: "Parallel downloads",
				Default: fmt.Sprintf("%d", cfg.ParallelDownloads),
			},
			Validate: validatePositiveInt,
		},
		{
			Name: "tmp_dir",
			Prompt: &survey.Input{
				Message: "Temporary directory",
				Default: cfg.TmpDir,
			},
			Validate: survey.Required,
		},
		{
			Name: "retry_count",
			Prompt: &survey.Input{
				Message: "Retry count",
				Default: fmt.Sprintf("%d", cfg.RetryCount),
			},
			Validate: validateNonNegativeInt,
		},
		{
			Name: "retry_backoff_max_seconds",
			Prompt: &survey.Input{
				Message: "Retry backoff max (seconds)",
				Default: fmt.Sprintf("%d", cfg.RetryBackoffMaxSec),
			},
			Validate: validatePositiveInt,
		},
		{
			Name: "user_agent",
			Prompt: &survey.Input{
				Message: "User agent",
				Default: cfg.UserAgent,
			},
		},
		{
			Name: "proxy",
			Prompt: &survey.Input{
				Message: "HTTP proxy (optional)",
				Default: cfg.Proxy,
			},
		},
		{
			Name: "tls_verify",
			Prompt: &survey.Confirm{
				Message: "Verify TLS certificates",
				Default: cfg.TLSVerify,
			},
		},
		{
			Name: "color_theme",
			Prompt: &survey.Select{
				Message: "Color theme",
				Options: theme.Names(),
				Default: cfg.ColorTheme,
			},
		},
		{
			Name: "max_episodes",
			Prompt: &survey.Input{
				Message: "Maximum episodes to display in list",
				Default: fmt.Sprintf("%d", cfg.MaxEpisodes),
			},
			Validate: validatePositiveInt,
		},
		{
			Name: "max_episode_description_lines",
			Prompt: &survey.Input{
				Message: "Maximum description lines in episode view",
				Default: fmt.Sprintf("%d", cfg.MaxEpisodeDescriptionLines),
			},
			Validate: validatePositiveInt,
		},
	}

	answers := map[string]interface{}{}
	select {
	case <-ctx.Done():
		return Config{}, ctx.Err()
	default:
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return Config{}, err
	}

	cfg.DownloadRoot = strings.TrimSpace(answers["download_root"].(string))
	cfg.ParallelDownloads = toInt(answers["parallel_downloads"])
	cfg.TmpDir = strings.TrimSpace(answers["tmp_dir"].(string))
	cfg.RetryCount = toInt(answers["retry_count"])
	cfg.RetryBackoffMaxSec = toInt(answers["retry_backoff_max_seconds"])
	cfg.UserAgent = strings.TrimSpace(answers["user_agent"].(string))
	cfg.Proxy = strings.TrimSpace(answers["proxy"].(string))
	cfg.TLSVerify = answers["tls_verify"].(bool)
	if themeName, ok := answers["color_theme"].(string); ok {
		cfg.ColorTheme = themeName
	}
	cfg.MaxEpisodes = toInt(answers["max_episodes"])
	cfg.MaxEpisodeDescriptionLines = toInt(answers["max_episode_description_lines"])

	return cfg, nil
}

func validatePositiveInt(ans interface{}) error {
	v := strings.TrimSpace(ans.(string))
	if v == "" {
		return errors.New("value required")
	}
	i, err := parseInt(v)
	if err != nil {
		return err
	}
	if i <= 0 {
		return errors.New("must be greater than zero")
	}
	return nil
}

func validateNonNegativeInt(ans interface{}) error {
	v := strings.TrimSpace(ans.(string))
	if v == "" {
		return errors.New("value required")
	}
	i, err := parseInt(v)
	if err != nil {
		return err
	}
	if i < 0 {
		return errors.New("must be zero or positive")
	}
	return nil
}

func parseInt(value string) (int, error) {
	var i int
	_, err := fmt.Sscanf(value, "%d", &i)
	if err != nil {
		return 0, errors.New("must be a number")
	}
	return i, nil
}

func toInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case string:
		i, _ := parseInt(v)
		return i
	default:
		return 0
	}
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
	}
	return path, nil
}

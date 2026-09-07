package worker

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/jbolus-owens/factory/internal/protocol"
	"github.com/jbolus-owens/factory/internal/statepath"
)

const (
	defaultServer        = "http://127.0.0.1:7337"
	defaultMaxConcurrent = 10
	defaultEffort        = "medium"
	plannerRole          = "planner"
	executorRole         = "executor"
	reviewerRole         = "reviewer"
	legacyReviewer       = "legacy-reviewer"
)

type RepositoryConfig struct {
	Path       string `toml:"path"`
	BaseBranch string `toml:"base_branch"`
}

type ReviewConfig struct {
	Enabled bool   `toml:"enabled"`
	Runtime string `toml:"runtime"`
	Model   string `toml:"model"`
}

type ProfileConfig struct {
	Name            string `toml:"name"`
	Adapter         string `toml:"adapter"`
	Provider        string `toml:"provider"`
	Model           string `toml:"model"`
	ReasoningEffort string `toml:"reasoning_effort"`
}

type RolesConfig struct {
	Planner  string `toml:"planner"`
	Executor string `toml:"executor"`
	Reviewer string `toml:"reviewer"`
}

type Config struct {
	Server          string                      `toml:"server"`
	Name            string                      `toml:"name"`
	Labels          map[string]string           `toml:"labels"`
	EnrollmentToken string                      `toml:"enrollment_token"`
	CACertificate   string                      `toml:"ca_certificate"`
	Runtime         string                      `toml:"runtime"`
	Runtimes        []string                    `toml:"runtimes"`
	Profiles        []ProfileConfig             `toml:"profiles"`
	Roles           RolesConfig                 `toml:"roles"`
	Review          ReviewConfig                `toml:"review"`
	MaxConcurrent   int                         `toml:"max_concurrent"`
	DataDirectory   string                      `toml:"data_directory"`
	SourceAccess    []string                    `toml:"source_access"`
	Repositories    map[string]RepositoryConfig `toml:"repositories"`
	path            string
}

type Repository struct {
	Key             string
	Path            string
	RemoteIdentity  string
	BaseCommit      string
	BaseBranch      string
	coordinationKey string
}

func LoadConfig(path string) (Config, error) {
	var config Config
	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, fmt.Errorf("load worker configuration: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Config{}, fmt.Errorf("unknown worker configuration fields: %s", strings.Join(keys, ", "))
	}
	if config.Server == "" {
		config.Server = defaultServer
	}
	config.Runtime = strings.ToLower(strings.TrimSpace(config.Runtime))
	config.Review.Runtime = strings.ToLower(strings.TrimSpace(config.Review.Runtime))
	config.Review.Model = strings.TrimSpace(config.Review.Model)
	for index := range config.Profiles {
		profile := &config.Profiles[index]
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Adapter = strings.ToLower(strings.TrimSpace(profile.Adapter))
		profile.Provider = strings.TrimSpace(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		profile.ReasoningEffort = strings.ToLower(strings.TrimSpace(profile.ReasoningEffort))
		if profile.ReasoningEffort == "" {
			profile.ReasoningEffort = defaultEffort
		}
	}
	config.Roles.Planner = strings.TrimSpace(config.Roles.Planner)
	config.Roles.Executor = strings.TrimSpace(config.Roles.Executor)
	config.Roles.Reviewer = strings.TrimSpace(config.Roles.Reviewer)
	if !metadata.IsDefined("roles") && config.Review.Enabled {
		if config.Review.Runtime != protocol.RuntimeCodex && config.Review.Runtime != protocol.RuntimeClaudeCode {
			return Config{}, errors.New("review.runtime must be codex or claude-code")
		}
		if config.Review.Model == "" {
			return Config{}, errors.New("review.model is required when review is enabled")
		}
		config.Profiles = append(config.Profiles, ProfileConfig{
			Name: legacyReviewer, Adapter: config.Review.Runtime, Provider: config.Review.Runtime,
			Model: config.Review.Model, ReasoningEffort: defaultEffort,
		})
		config.Roles.Reviewer = legacyReviewer
	}
	config.EnrollmentToken = strings.TrimSpace(config.EnrollmentToken)
	config.CACertificate = strings.TrimSpace(config.CACertificate)
	if config.CACertificate != "" && !filepath.IsAbs(config.CACertificate) {
		config.CACertificate = filepath.Join(filepath.Dir(path), config.CACertificate)
	}
	for index := range config.Runtimes {
		config.Runtimes[index] = strings.ToLower(strings.TrimSpace(config.Runtimes[index]))
	}
	config.Runtimes = configuredRuntimes(config)
	if config.Runtime == "" {
		config.Runtime = config.Runtimes[0]
	}
	if !metadata.IsDefined("max_concurrent") {
		config.MaxConcurrent = defaultMaxConcurrent
	}
	config.path, err = filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve worker configuration path: %w", err)
	}
	dataDirectoryDefaulted := !metadata.IsDefined("data_directory")
	if dataDirectoryDefaulted {
		config.DataDirectory, err = defaultDataDirectory(config.path)
		if err != nil {
			return Config{}, err
		}
	}
	for index := range config.SourceAccess {
		config.SourceAccess[index] = strings.ToLower(strings.TrimSpace(config.SourceAccess[index]))
	}
	if !dataDirectoryDefaulted && strings.TrimSpace(config.DataDirectory) != "" && !filepath.IsAbs(config.DataDirectory) {
		config.DataDirectory = filepath.Join(filepath.Dir(path), config.DataDirectory)
	}
	for key, repository := range config.Repositories {
		if !filepath.IsAbs(repository.Path) {
			repository.Path = filepath.Join(filepath.Dir(path), repository.Path)
			config.Repositories[key] = repository
		}
	}
	return config, validateConfig(config)
}

func defaultDataDirectory(configPath string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(configPath), ".toml")
	if strings.TrimSpace(base) == "" || base == "." || base == ".." {
		return "", errors.New("derive data_directory: worker configuration filename has no usable basename after removing .toml")
	}
	return filepath.Join(filepath.Dir(configPath), "workers", base), nil
}

func validateConfig(config Config) error {
	if err := validateServerURL(config.Server); err != nil {
		return err
	}
	remote := strings.HasPrefix(config.Server, "https://")
	if !remote && (config.EnrollmentToken != "" || config.CACertificate != "") {
		return errors.New("enrollment_token and ca_certificate require a remote HTTPS server")
	}
	if config.EnrollmentToken != "" && (len(config.EnrollmentToken) < 32 || len(config.EnrollmentToken) > 1024) {
		return errors.New("enrollment_token must contain between 32 and 1024 bytes")
	}
	if strings.TrimSpace(config.Name) == "" || len(config.Name) > 200 {
		return errors.New("name is required and must be at most 200 bytes")
	}
	if len(config.Labels) > 20 {
		return errors.New("labels may contain at most 20 entries")
	}
	for key, value := range config.Labels {
		if strings.TrimSpace(key) == "" || key != strings.TrimSpace(key) || len(key) > 100 || len(strings.TrimSpace(value)) > 200 {
			return errors.New("label keys must be trimmed and at most 100 bytes; values must be at most 200 bytes")
		}
	}
	primaryRuntime := config.Runtime
	if primaryRuntime == "" {
		primaryRuntime = configuredRuntimes(config)[0]
	}
	if !protocol.SupportedRuntime(primaryRuntime) {
		return errors.New("runtime must be pi, codex, or claude-code")
	}
	seenRuntimes := make(map[string]bool, len(config.Runtimes))
	primaryFound := false
	for _, runtime := range configuredRuntimes(config) {
		if !protocol.SupportedRuntime(runtime) {
			return errors.New("runtimes may contain only pi, codex, or claude-code")
		}
		if seenRuntimes[runtime] {
			return fmt.Errorf("runtime %q is duplicated", runtime)
		}
		seenRuntimes[runtime] = true
		primaryFound = primaryFound || runtime == primaryRuntime
	}
	if !primaryFound {
		return errors.New("runtime must also appear in runtimes when both fields are set")
	}
	if err := validateProfiles(config, seenRuntimes); err != nil {
		return err
	}
	if config.MaxConcurrent < protocol.MinWorkerCapacity || config.MaxConcurrent > protocol.MaxWorkerCapacity {
		return fmt.Errorf("max_concurrent must be between %d and %d",
			protocol.MinWorkerCapacity, protocol.MaxWorkerCapacity)
	}
	if strings.TrimSpace(config.DataDirectory) == "" {
		return errors.New("data_directory is required")
	}
	seenSourceAccess := make(map[string]bool, len(config.SourceAccess))
	for _, source := range config.SourceAccess {
		if source != "github" {
			return fmt.Errorf("source_access %q is unsupported; v1 supports github", source)
		}
		if seenSourceAccess[source] {
			return fmt.Errorf("source_access %q is duplicated", source)
		}
		seenSourceAccess[source] = true
	}
	for key, repository := range config.Repositories {
		if strings.TrimSpace(key) == "" || len(key) > 200 || key != strings.TrimSpace(key) {
			return errors.New("repository keys are required, must not have surrounding whitespace, and must be at most 200 bytes")
		}
		if strings.TrimSpace(repository.Path) == "" {
			return fmt.Errorf("repository %q path is required", key)
		}
		if repository.BaseBranch != strings.TrimSpace(repository.BaseBranch) {
			return fmt.Errorf("repository %q base_branch must not have surrounding whitespace", key)
		}
		if len(repository.BaseBranch) > 244 {
			return fmt.Errorf("repository %q base_branch must be at most 244 bytes", key)
		}
	}
	return nil
}

func validateProfiles(config Config, runtimes map[string]bool) error {
	if len(config.Profiles) == 0 {
		return nil
	}
	profiles := make(map[string]ProfileConfig, len(config.Profiles))
	for _, profile := range config.Profiles {
		if profile.Name == "" || profile.Adapter == "" || profile.Provider == "" || profile.Model == "" {
			return errors.New("profiles require name, adapter, provider, and model")
		}
		if !protocol.SupportedRuntime(profile.Adapter) {
			return errors.New("profile adapter must be pi, codex, or claude-code")
		}
		if !runtimes[profile.Adapter] {
			return fmt.Errorf("profile %q adapter must appear in runtimes", profile.Name)
		}
		if !supportedEffort(profile.ReasoningEffort) {
			return fmt.Errorf("profile %q has invalid reasoning_effort", profile.Name)
		}
		if _, exists := profiles[profile.Name]; exists {
			return fmt.Errorf("profile %q is duplicated", profile.Name)
		}
		profiles[profile.Name] = profile
	}
	for _, role := range []string{config.Roles.Planner, config.Roles.Executor, config.Roles.Reviewer} {
		if role != "" {
			if _, exists := profiles[role]; !exists {
				return fmt.Errorf("role references unknown profile %q", role)
			}
		}
	}
	return nil
}

func supportedEffort(value string) bool {
	for _, effort := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if value == effort {
			return true
		}
	}
	return false
}

func (config Config) profile(role string) (ProfileConfig, error) {
	name := map[string]string{plannerRole: config.Roles.Planner, executorRole: config.Roles.Executor, reviewerRole: config.Roles.Reviewer}[role]
	if name == "" {
		return ProfileConfig{}, fmt.Errorf("%s role is not configured", role)
	}
	for _, profile := range config.Profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	return ProfileConfig{}, fmt.Errorf("%s role references unknown profile %q", role, name)
}

func (config Config) roleRuntimes() []string {
	runtimes := make([]string, 0, len(config.Profiles))
	seen := make(map[string]bool, len(config.Profiles))
	for _, role := range []string{plannerRole, executorRole, reviewerRole} {
		profile, err := config.profile(role)
		if err != nil || seen[profile.Adapter] {
			continue
		}
		seen[profile.Adapter] = true
		runtimes = append(runtimes, profile.Adapter)
	}
	return runtimes
}

func (config Config) healthProfiles() []ProfileConfig {
	return config.Profiles
}

func (manager *Manager) executorProfile(runtime string) (ProfileConfig, error) {
	if len(manager.config.Profiles) == 0 || manager.config.Roles.Executor == "" {
		return ProfileConfig{Adapter: runtime}, nil
	}
	profile, err := manager.config.profile(executorRole)
	if err != nil {
		return ProfileConfig{}, err
	}
	if profile.Adapter != runtime {
		return ProfileConfig{}, fmt.Errorf("executor profile adapter %q does not match claimed runtime %q", profile.Adapter, runtime)
	}
	return profile, nil
}

func configuredRuntimes(config Config) []string {
	if len(config.Runtimes) != 0 {
		return append([]string(nil), config.Runtimes...)
	}
	if config.Runtime != "" {
		return []string{config.Runtime}
	}
	return []string{protocol.RuntimeCodex}
}

func validateServerURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse server URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("server must be an HTTP or HTTPS URL without credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("server URL must not contain a path")
	}
	host := parsed.Hostname()
	if host == "" || parsed.Port() == "" {
		return errors.New("server URL must include a host and port")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return errors.New("server URL host must be loopback")
		}
		return nil
	}
	if !strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return errors.New("server URL host must be a loopback IP or localhost")
	}
	addresses, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve server URL host: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("server URL host resolved to no addresses")
	}
	for _, address := range addresses {
		if !address.IsLoopback() {
			return errors.New("server URL host must resolve only to loopback")
		}
	}
	return nil
}

func resolveDataDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if marker, found, err := statepath.FindRetiredDatabaseMarker(absolute); err != nil {
		return "", err
	} else if found {
		return "", fmt.Errorf("refusing a worker data directory below retired local state at %s", marker)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("data_directory must be a real directory, not a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect data directory: %w", err)
	}
	canonical, err := statepath.CanonicalProspective(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize data directory: %w", err)
	}
	if marker, found, err := statepath.FindRetiredDatabaseMarker(canonical); err != nil {
		return "", err
	} else if found {
		return "", fmt.Errorf("refusing a worker data directory below retired local state at %s", marker)
	}
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("data_directory must be a real directory, not a symlink")
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize data directory: %w", err)
	}
	if err := os.Chmod(canonical, 0o700); err != nil {
		return "", fmt.Errorf("protect data directory: %w", err)
	}
	return canonical, nil
}

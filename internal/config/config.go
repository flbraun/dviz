// Package config resolves, parses and validates the dviz.yml configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the name of the configuration file in every search location.
const FileName = "dviz.yml"

const (
	DefaultListen   = "127.0.0.1:8080"
	DefaultHostName = "local"
	DefaultHostURL  = "unix:///var/run/docker.sock"
)

// Config is the complete application configuration.
type Config struct {
	Listen string `yaml:"listen"`
	Hosts  []Host `yaml:"hosts"`

	// Source is the file the config was loaded from; empty when defaults are used.
	Source string `yaml:"-"`
}

// Host is one Docker daemon to connect to.
type Host struct {
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	URL         string `yaml:"url"`
	TLS         *TLS   `yaml:"tls"`
	SSH         *SSH   `yaml:"ssh"`
}

// SSH holds settings for ssh:// hosts. The URL has the form
// ssh://[user@]host[:port][/path/to/docker.sock].
type SSH struct {
	// Method selects how to authenticate: password, agent or key.
	Method string `yaml:"method"`
	// Password is a file holding the login password (method password) or the passphrase of
	// a protected identity_file (method key).
	Password string `yaml:"password"`
	// IdentityFile is the private key used with method key.
	IdentityFile string `yaml:"identity_file"`
	// KnownHosts is the known_hosts file used to verify the server; default ~/.ssh/known_hosts.
	KnownHosts string `yaml:"known_hosts"`
	// InsecureIgnoreHostKey disables host key verification.
	InsecureIgnoreHostKey bool `yaml:"insecure_ignore_host_key"`
}

// SSH authentication methods.
const (
	SSHMethodPassword = "password"
	SSHMethodAgent    = "agent"
	SSHMethodKey      = "key"
)

// Default values for ssh:// hosts.
const (
	DefaultSSHPort   = "22"
	DefaultSSHSocket = "/var/run/docker.sock"
)

// TLS holds client TLS settings for tcp:// hosts.
type TLS struct {
	CA                 string `yaml:"ca"`
	Cert               string `yaml:"cert"`
	Key                string `yaml:"key"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

var hostNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// DefaultCandidates returns the search order for the config file:
// $(pwd)/dviz.yml, ~/.config/dviz/dviz.yml, /etc/dviz/dviz.yml.
func DefaultCandidates() []string {
	var out []string
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, FileName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "dviz", FileName))
	}
	return append(out, filepath.Join("/etc", "dviz", FileName))
}

// Default returns the configuration used when no config file exists.
func Default() *Config {
	c := &Config{}
	c.applyDefaults()
	return c
}

// Resolve returns the first candidate that exists as a regular file, or "" if none does.
func Resolve(candidates []string) (string, error) {
	for _, p := range candidates {
		fi, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", p, err)
		}
		if fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", nil
}

// Load resolves the config file from candidates and parses it. Without a file it returns Default().
func Load(candidates []string) (*Config, error) {
	path, err := Resolve(candidates)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	c, err := Parse(data, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Source = path
	return c, nil
}

// Parse decodes and validates YAML config data. Relative TLS paths resolve against baseDir.
func Parse(data []byte, baseDir string) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	for i := range c.Hosts {
		if t := c.Hosts[i].TLS; t != nil {
			t.CA = resolvePath(baseDir, t.CA)
			t.Cert = resolvePath(baseDir, t.Cert)
			t.Key = resolvePath(baseDir, t.Key)
		}
		if s := c.Hosts[i].SSH; s != nil {
			s.IdentityFile = resolvePath(baseDir, s.IdentityFile)
			s.Password = resolvePath(baseDir, s.Password)
			s.KnownHosts = resolvePath(baseDir, s.KnownHosts)
		}
	}
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if len(c.Hosts) == 0 {
		c.Hosts = []Host{{Name: DefaultHostName, URL: DefaultHostURL}}
	}
	for i := range c.Hosts {
		if c.Hosts[i].DisplayName == "" {
			c.Hosts[i].DisplayName = c.Hosts[i].Name
		}
	}
}

func (c *Config) validate() error {
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("listen %q: invalid port", c.Listen)
	}
	if host != "" && host != "localhost" && net.ParseIP(host) == nil {
		return fmt.Errorf("listen %q: host must be an IP address or localhost", c.Listen)
	}

	seen := map[string]bool{}
	for i, h := range c.Hosts {
		if !hostNameRE.MatchString(h.Name) {
			return fmt.Errorf("hosts[%d]: name %q must match %s", i, h.Name, hostNameRE)
		}
		if seen[h.Name] {
			return fmt.Errorf("hosts[%d]: duplicate name %q", i, h.Name)
		}
		seen[h.Name] = true
		if err := h.validateURL(); err != nil {
			return fmt.Errorf("hosts[%d] (%s): %w", i, h.Name, err)
		}
	}
	return nil
}

func (h Host) validateURL() error {
	u, err := url.Parse(h.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	if h.SSH != nil && u.Scheme != "ssh" {
		return errors.New("ssh settings are only supported for ssh:// urls")
	}
	switch u.Scheme {
	case "unix":
		if u.Path == "" {
			return fmt.Errorf("url %q: missing socket path", h.URL)
		}
		if h.TLS != nil {
			return errors.New("tls is only supported for tcp:// urls")
		}
	case "tcp":
		if _, _, err := net.SplitHostPort(u.Host); err != nil {
			return fmt.Errorf("url %q: %w", h.URL, err)
		}
		if t := h.TLS; t != nil && (t.Cert == "") != (t.Key == "") {
			return errors.New("tls: cert and key must be set together")
		}
	case "ssh":
		if h.TLS != nil {
			return errors.New("tls is not supported for ssh:// urls; ssh encrypts the connection")
		}
		if _, hasPassword := u.User.Password(); hasPassword {
			return fmt.Errorf("url %q: passwords are not supported; use a key or ssh-agent", h.Address())
		}
		if u.Hostname() == "" {
			return fmt.Errorf("url %q: missing host", h.URL)
		}
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("url %q: invalid port", h.URL)
			}
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("url %q: query and fragment are not supported", h.URL)
		}
		return h.SSH.validate()
	default:
		return fmt.Errorf("url %q: scheme must be unix, tcp or ssh", h.URL)
	}
	return nil
}

func (s *SSH) validate() error {
	if s == nil || s.Method == "" {
		return fmt.Errorf("ssh.method is required for ssh:// urls (%s, %s or %s)", SSHMethodPassword, SSHMethodAgent, SSHMethodKey)
	}
	switch s.Method {
	case SSHMethodPassword:
		if s.Password == "" {
			return errors.New("ssh.password (a file containing the password) is required for method password")
		}
		if s.IdentityFile != "" {
			return errors.New("ssh.identity_file is only used with method key")
		}
	case SSHMethodAgent:
		if s.Password != "" || s.IdentityFile != "" {
			return errors.New("ssh.password and ssh.identity_file are not used with method agent")
		}
	case SSHMethodKey:
		if s.IdentityFile == "" {
			return errors.New("ssh.identity_file is required for method key")
		}
	default:
		return fmt.Errorf("ssh.method %q must be %s, %s or %s", s.Method, SSHMethodPassword, SSHMethodAgent, SSHMethodKey)
	}
	return nil
}

// SSHTarget returns the user (may be empty), host:port and remote socket path of an ssh:// URL.
func (h Host) SSHTarget() (user, addr, socket string, err error) {
	u, err := url.Parse(h.URL)
	if err != nil || u.Scheme != "ssh" {
		return "", "", "", fmt.Errorf("not an ssh url: %q", h.URL)
	}
	port := u.Port()
	if port == "" {
		port = DefaultSSHPort
	}
	socket = u.Path
	if socket == "" || socket == "/" {
		socket = DefaultSSHSocket
	}
	return u.User.Username(), net.JoinHostPort(u.Hostname(), port), socket, nil
}

// Address returns the URL shown to users: the socket path or tcp address, never credentials.
func (h Host) Address() string {
	u, err := url.Parse(h.URL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "unix":
		return "unix://" + u.Path
	case "ssh":
		user, addr, socket, err := h.SSHTarget()
		if err != nil {
			return ""
		}
		if user != "" {
			user += "@"
		}
		return "ssh://" + user + addr + socket
	}
	return "tcp://" + u.Host
}

func resolvePath(baseDir, p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return p
}

package store

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"lontar/internal/driver"
)

// Tag classifies a connection's environment. It drives the colour strip in
// the UI and, later, safe-mode defaults.
type Tag string

// Supported tags.
const (
	TagNone    Tag = "none"
	TagLocal   Tag = "local"
	TagDev     Tag = "dev"
	TagStaging Tag = "staging"
	TagProd    Tag = "prod"
)

// Tags lists the valid tags in display order.
var Tags = []Tag{TagNone, TagLocal, TagDev, TagStaging, TagProd}

// Valid reports whether t is a known tag.
func (t Tag) Valid() bool {
	for _, v := range Tags {
		if t == v {
			return true
		}
	}
	return false
}

// SafeMode guards a connection against accidental writes.
type SafeMode string

// Safe modes.
const (
	// SafeOff: no restrictions.
	SafeOff SafeMode = "off"
	// SafeConfirm: SQL editor runs that contain a write and every commit of
	// staged grid edits need an explicit confirmation in the UI.
	SafeConfirm SafeMode = "confirm"
	// SafeReadOnly: the session is opened read-only at the database level
	// (PostgreSQL default_transaction_read_only, MySQL/MariaDB
	// transaction_read_only / tx_read_only, SQLite mode=ro) and the app
	// refuses to apply grid edits.
	SafeReadOnly SafeMode = "readonly"
)

// SafeModes lists the valid modes in display order.
var SafeModes = []SafeMode{SafeOff, SafeConfirm, SafeReadOnly}

// Valid reports whether m is a known mode.
func (m SafeMode) Valid() bool {
	for _, v := range SafeModes {
		if m == v {
			return true
		}
	}
	return false
}

// SSH authentication methods (SSHConfig.AuthMethod).
const (
	SSHAuthPassword = "password"
	SSHAuthKey      = "key"
	SSHAuthAgent    = "agent"
)

// DefaultSSHPort is used when SSHConfig.Port is 0.
const DefaultSSHPort = 22

// SSHConfig describes an SSH tunnel to the database host. Secrets (the SSH
// password and the key passphrase) are kept in the SecretStore under
// SecretKey(id, SecretSSHPassword / SecretSSHPassphrase), never here.
type SSHConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user,omitempty"`
	// AuthMethod is "password", "key" or "agent".
	AuthMethod string `json:"authMethod,omitempty"`
	// KeyPath is the private key file for "key" ("~" is expanded).
	KeyPath string `json:"keyPath,omitempty"`
}

// SSL modes (SSLConfig.Mode). See driver.TLSDisable etc. for their meaning.
const (
	SSLDisable    = driver.TLSDisable
	SSLPreferred  = driver.TLSPreferred // MySQL/MariaDB only
	SSLRequire    = driver.TLSRequire
	SSLVerifyCA   = driver.TLSVerifyCA
	SSLVerifyFull = driver.TLSVerifyFull
)

// SSLConfig describes TLS settings. An empty Mode leaves TLS to the
// driver options (sslmode / tls) and defaults, as before M6; a set Mode
// wins over those options.
type SSLConfig struct {
	Mode     string `json:"mode,omitempty"`
	CAFile   string `json:"caFile,omitempty"`
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// Connection is a saved connection. It never contains a password;
// HasPassword reports whether one is kept in the SecretStore.
type Connection struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Kind     driver.Kind `json:"kind"`
	Host     string      `json:"host,omitempty"`
	Port     int         `json:"port,omitempty"`
	User     string      `json:"user,omitempty"`
	Database string      `json:"database,omitempty"`

	// SQLite.
	FilePath        string `json:"filePath,omitempty"`
	ReadOnly        bool   `json:"readOnly,omitempty"`
	CreateIfMissing bool   `json:"createIfMissing,omitempty"`

	Options map[string]string `json:"options,omitempty"`
	Tag     Tag               `json:"tag"`
	Color   string            `json:"color,omitempty"`
	// SafeMode defaults to SafeOff (Normalize). The UI suggests
	// SafeConfirm for production-tagged connections.
	SafeMode SafeMode `json:"safeMode"`

	// Optional, added in later milestones; nil means "not configured".
	SSH *SSHConfig `json:"ssh,omitempty"`
	SSL *SSLConfig `json:"ssl,omitempty"`

	// HasPassword is maintained by Store (values passed to Save are
	// ignored). It lets the UI show "saved" without touching the keychain.
	HasPassword bool `json:"hasPassword"`
	// HasSSHPassword and HasSSHPassphrase report saved SSH secrets, like
	// HasPassword (maintained by Store; the secrets are never returned).
	HasSSHPassword   bool `json:"hasSshPassword"`
	HasSSHPassphrase bool `json:"hasSshPassphrase"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DriverConfig converts c into the driver package's configuration.
func (c Connection) DriverConfig() driver.Config {
	return driver.Config{
		Kind:            c.Kind,
		Host:            c.Host,
		Port:            c.Port,
		User:            c.User,
		Database:        c.Database,
		FilePath:        c.FilePath,
		CreateIfMissing: c.CreateIfMissing,
		ReadOnly:        c.ReadOnly || c.SafeMode == SafeReadOnly,
		Options:         c.Options,
		TLS:             c.tls(),
	}
}

func (c Connection) tls() *driver.TLSConfig {
	if c.SSL == nil || c.SSL.Mode == "" || c.Kind.IsFileBased() {
		return nil
	}
	return &driver.TLSConfig{Mode: c.SSL.Mode, CAFile: c.SSL.CAFile, CertFile: c.SSL.CertFile, KeyFile: c.SSL.KeyFile}
}

// SSHEnabled reports whether the connection goes through an SSH tunnel.
func (c Connection) SSHEnabled() bool {
	return c.SSH != nil && c.SSH.Enabled && !c.Kind.IsFileBased()
}

// ValidationError is returned for invalid connection settings. Its message
// is meant to be shown to the user as is.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// MaxNameLen bounds connection names.
const MaxNameLen = 200

// Normalize trims text fields, defaults the tag and clears fields that do
// not apply to the kind (e.g. host for SQLite).
func (c *Connection) Normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.Database = strings.TrimSpace(c.Database)
	c.FilePath = strings.TrimSpace(c.FilePath)
	c.Color = strings.TrimSpace(c.Color)
	if c.Tag == "" {
		c.Tag = TagNone
	}
	if c.SafeMode == "" {
		c.SafeMode = SafeOff
	}
	if c.Kind.IsFileBased() {
		c.Host, c.Port, c.User, c.Database = "", 0, "", ""
		// No network: SSH and SSL do not apply.
		c.SSH, c.SSL = nil, nil
	} else {
		c.FilePath, c.ReadOnly, c.CreateIfMissing = "", false, false
	}
	if len(c.Options) == 0 {
		c.Options = nil
	}
	if c.SSH != nil {
		c.SSH.Host = strings.TrimSpace(c.SSH.Host)
		c.SSH.User = strings.TrimSpace(c.SSH.User)
		c.SSH.KeyPath = strings.TrimSpace(c.SSH.KeyPath)
		if c.SSH.AuthMethod == "" {
			c.SSH.AuthMethod = SSHAuthPassword
		}
		if c.SSH.Port == 0 {
			c.SSH.Port = DefaultSSHPort
		}
		if !c.SSH.Enabled && c.SSH.Host == "" && c.SSH.User == "" && c.SSH.KeyPath == "" {
			c.SSH = nil
		}
	}
	if c.SSL != nil {
		c.SSL.Mode = strings.TrimSpace(c.SSL.Mode)
		c.SSL.CAFile = strings.TrimSpace(c.SSL.CAFile)
		c.SSL.CertFile = strings.TrimSpace(c.SSL.CertFile)
		c.SSL.KeyFile = strings.TrimSpace(c.SSL.KeyFile)
		if *c.SSL == (SSLConfig{}) {
			c.SSL = nil
		}
	}
}

// Validate checks c (after Normalize) and returns a *ValidationError.
func (c *Connection) Validate() error {
	switch {
	case c.Name == "":
		return invalid("name", "name is required")
	case len(c.Name) > MaxNameLen:
		return invalid("name", "name must be at most %d characters", MaxNameLen)
	case !c.Kind.Valid():
		return invalid("kind", "unsupported database type %q", c.Kind)
	case !c.Tag.Valid():
		return invalid("tag", "unknown tag %q", c.Tag)
	case !c.SafeMode.Valid():
		return invalid("safeMode", "unknown safe mode %q", c.SafeMode)
	case c.Color != "" && !colorRe.MatchString(c.Color):
		return invalid("color", "colour must look like #rrggbb")
	}
	if c.Kind.IsFileBased() {
		if c.FilePath == "" {
			return invalid("filePath", "database file is required")
		}
	} else {
		if c.Host == "" {
			return invalid("host", "host is required")
		}
		if c.Port < 0 || c.Port > 65535 {
			return invalid("port", "port must be between 1 and 65535")
		}
	}
	for k := range c.Options {
		if strings.TrimSpace(k) == "" {
			return invalid("options", "option names must not be empty")
		}
	}
	if err := c.validateSSH(); err != nil {
		return err
	}
	return c.validateSSL()
}

func (c *Connection) validateSSH() error {
	s := c.SSH
	if s == nil {
		return nil
	}
	switch s.AuthMethod {
	case SSHAuthPassword, SSHAuthKey, SSHAuthAgent:
	default:
		return invalid("ssh.authMethod", "unknown SSH authentication method %q", s.AuthMethod)
	}
	if s.Port < 1 || s.Port > 65535 {
		return invalid("ssh.port", "SSH port must be between 1 and 65535")
	}
	if !s.Enabled {
		return nil
	}
	switch {
	case s.Host == "":
		return invalid("ssh.host", "SSH host is required")
	case s.User == "":
		return invalid("ssh.user", "SSH user is required")
	case s.AuthMethod == SSHAuthKey && s.KeyPath == "":
		return invalid("ssh.keyPath", "SSH private key file is required")
	}
	return nil
}

func (c *Connection) validateSSL() error {
	s := c.SSL
	if s == nil {
		return nil
	}
	if s.Mode != "" && !slices.Contains(driver.TLSModes(c.Kind), s.Mode) {
		return invalid("ssl.mode", "SSL mode %q is not supported for %s", s.Mode, c.Kind)
	}
	if (s.CertFile == "") != (s.KeyFile == "") {
		return invalid("ssl.certFile", "a client certificate needs both the certificate and the key file")
	}
	return nil
}

// clone returns a deep copy of c.
func (c Connection) clone() Connection {
	if c.Options != nil {
		opts := make(map[string]string, len(c.Options))
		for k, v := range c.Options {
			opts[k] = v
		}
		c.Options = opts
	}
	if c.SSH != nil {
		s := *c.SSH
		c.SSH = &s
	}
	if c.SSL != nil {
		s := *c.SSL
		c.SSL = &s
	}
	return c
}

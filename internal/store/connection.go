package store

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"dbedit/internal/driver"
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

// SSHConfig describes an SSH tunnel. Reserved for milestone M6: it is
// persisted but not applied yet, and conn refuses to connect while it is
// enabled so that a connection never silently bypasses its tunnel. Secrets
// (SSH password / key passphrase) will live in the SecretStore under
// SecretKey(id, "ssh"), never in this struct.
type SSHConfig struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user,omitempty"`
	AuthMethod string `json:"authMethod,omitempty"` // "password", "key" or "agent"
	KeyPath    string `json:"keyPath,omitempty"`
}

// SSLConfig describes TLS settings. Reserved for milestone M6, like
// SSHConfig: conn refuses to connect when a mode other than "" or "disable"
// is set, rather than connecting without the requested TLS.
type SSLConfig struct {
	Mode     string `json:"mode,omitempty"` // disable, prefer, require, verify-ca, verify-full
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

	// Optional, added in later milestones; nil means "not configured".
	SSH *SSHConfig `json:"ssh,omitempty"`
	SSL *SSLConfig `json:"ssl,omitempty"`

	// HasPassword is maintained by Store (values passed to Save are
	// ignored). It lets the UI show "saved" without touching the keychain.
	HasPassword bool `json:"hasPassword"`

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
		ReadOnly:        c.ReadOnly,
		Options:         c.Options,
	}
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
	if c.Kind.IsFileBased() {
		c.Host, c.Port, c.User, c.Database = "", 0, "", ""
	} else {
		c.FilePath, c.ReadOnly, c.CreateIfMissing = "", false, false
	}
	if len(c.Options) == 0 {
		c.Options = nil
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

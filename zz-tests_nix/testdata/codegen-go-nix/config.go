package config

//go:generate tommy generate
type Config struct {
	Name   string            `toml:"name"`
	Port   int               `toml:"port,omitempty"`
	Tags   []string          `toml:"tags"`
	Labels map[string]string `toml:"labels"`
}

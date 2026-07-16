package config

import "path/filepath"

// Config holds process configuration for gaderno serve.
type Config struct {
	Root   string
	Listen string
	Token  string
	Kernel string // default kernelspec name when notebook metadata lacks one
}

// Default returns localhost-first defaults.
func Default() Config {
	return Config{
		Root:   ".",
		Listen: "127.0.0.1:8080",
		Kernel: "python3",
	}
}

// AbsRoot returns the absolute workspace root.
func (c Config) AbsRoot() (string, error) {
	return filepath.Abs(c.Root)
}

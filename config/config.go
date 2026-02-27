package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Speaker struct {
	Name string `yaml:"name" json:"name"`
	IP   string `yaml:"ip" json:"ip"`
}

type Config struct {
	Speakers   []Speaker `yaml:"speakers"`
	AudioFile  string    `yaml:"audio_file"`
	AudioURL   string    `yaml:"audio_url"`
	ListenAddr string    `yaml:"listen_addr"`
	Auth       struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"auth"`
	SecretPath string `yaml:"secret_path"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Speakers) == 0 {
		return fmt.Errorf("at least one speaker is required")
	}
	for i, s := range c.Speakers {
		if s.Name == "" {
			return fmt.Errorf("speaker %d: name is required", i)
		}
		if s.IP == "" {
			return fmt.Errorf("speaker %d (%s): ip is required", i, s.Name)
		}
	}

	if c.AudioFile == "" {
		return fmt.Errorf("audio_file is required")
	}

	if c.AudioURL == "" {
		return fmt.Errorf("audio_url is required (public base URL for Chromecast to fetch audio)")
	}

	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}

	if c.SecretPath == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generating secret path: %w", err)
		}
		c.SecretPath = hex.EncodeToString(b)
	}

	return nil
}

func (c *Config) FullAudioURL() string {
	return fmt.Sprintf("%s/audio/%s/whitenoise.mp3", c.AudioURL, c.SecretPath)
}

func (c *Config) HasAuth() bool {
	return c.Auth.Username != "" && c.Auth.Password != ""
}

func (c *Config) SpeakerByIP(ip string) *Speaker {
	for i := range c.Speakers {
		if c.Speakers[i].IP == ip {
			return &c.Speakers[i]
		}
	}
	return nil
}

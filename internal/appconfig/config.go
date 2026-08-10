// Package appconfig loads the optional sidecar runtime configuration.
package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const SchemaVersion = "1"

type Config struct {
	SchemaVersion string        `json:"schema_version"`
	Model         RuntimeConfig `json:"model,omitempty"`
	Whisper       WhisperConfig `json:"whisper,omitempty"`
}

type RuntimeConfig struct {
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Persistent bool     `json:"persistent,omitempty"`
}

type WhisperConfig struct {
	Command   string   `json:"command,omitempty"`
	ModelPath string   `json:"model_path,omitempty"`
	Args      []string `json:"args,omitempty"`
}

func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	return Decode(file)
}

func Decode(r io.Reader) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1024*1024+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > 1024*1024 {
		return Config{}, fmt.Errorf("config exceeds 1 MiB")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("decode config: multiple JSON values")
	}
	if config.SchemaVersion != SchemaVersion {
		return Config{}, fmt.Errorf("unsupported config schema_version %q", config.SchemaVersion)
	}
	if (config.Whisper.Command == "") != (config.Whisper.ModelPath == "") {
		return Config{}, fmt.Errorf("whisper.command and whisper.model_path must be set together")
	}
	if config.Model.Persistent && config.Model.Command == "" {
		return Config{}, fmt.Errorf("model.persistent requires model.command")
	}
	return config, nil
}

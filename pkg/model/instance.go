package model

import "fmt"

type DBType string

const (
	MySQL      DBType = "mysql"
	PostgreSQL DBType = "postgresql"
	Redis      DBType = "redis"
	MongoDB    DBType = "mongodb"
)

type TLSConfig struct {
	Enabled        bool   `yaml:"enabled"`
	CACertPath     string `yaml:"ca_cert_path"`
	ClientCertPath string `yaml:"client_cert_path"`
	ClientKeyPath  string `yaml:"client_key_path"`
	SkipVerify     bool   `yaml:"skip_verify"`
}

type InstanceConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Host     string            `yaml:"host" json:"host"`
	Port     int               `yaml:"port" json:"port"`
	Type     DBType            `yaml:"type" json:"type"`
	User     string            `yaml:"user" json:"user"`
	Password string            `yaml:"password" json:"password"`
	Database string            `yaml:"database" json:"database"`
	Options  map[string]string `yaml:"options" json:"options"`
	TLS      *TLSConfig        `yaml:"tls" json:"tls"`
}

func (i InstanceConfig) Address() string {
	return fmt.Sprintf("%s:%d", i.Host, i.Port)
}

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
	Enabled        bool
	CACertPath     string
	ClientCertPath string
	ClientKeyPath  string
	SkipVerify     bool
}

type InstanceConfig struct {
	Name     string            `yaml:"name"`
	Host     string            `yaml:"host"`
	Port     int               `yaml:"port"`
	Type     DBType            `yaml:"type"`
	User     string            `yaml:"user"`
	Password string            `yaml:"password"`
	Database string            `yaml:"database"`
	Options  map[string]string `yaml:"options"`
	TLS      *TLSConfig        `yaml:"tls"`
}

func (i InstanceConfig) Address() string {
	return fmt.Sprintf("%s:%d", i.Host, i.Port)
}

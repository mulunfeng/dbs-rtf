package mysql

import (
	"fmt"
	"net/url"

	"github.com/dbs-rtf/agent/pkg/model"
)

func buildDSN(cfg model.InstanceConfig) string {
	params := url.Values{}
	params.Set("parseTime", "true")
	if cfg.Database != "" {
		params.Set("database", cfg.Database)
	}
	if cfg.TLS != nil && cfg.TLS.Enabled {
		params.Set("tls", "true")
	}
	for k, v := range cfg.Options {
		params.Set(k, v)
	}

	query := params.Encode()
	user := cfg.User
	pass := url.QueryEscape(cfg.Password)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", user, pass, cfg.Host, cfg.Port)
	if query != "" {
		dsn += "?" + query
	}
	return dsn
}

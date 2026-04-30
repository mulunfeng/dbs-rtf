package monitor

import "time"

type MonitorConfig struct {
	Interval            time.Duration
	PingTimeout         time.Duration
	ConsecutiveFailures int
}

type FailoverConfig struct {
	Cooldown       time.Duration
	DryRun         bool
	NotifyBefore   bool
	ProxySwitchURL string
	// MasterHostnameMap maps instance address (host:port) to the hostname
	// that should be used inside MySQL containers for CHANGE MASTER TO.
	// e.g. "127.0.0.1:3306" -> "mysql-primary", "127.0.0.1:3307" -> "mysql-replica"
	MasterHostnameMap map[string]string
}

type NotificationConfig struct {
	WebhookURL string
	LogOnly    bool
}

type HAConfig struct {
	Enabled      bool
	Monitor      MonitorConfig
	Failover     FailoverConfig
	Notification NotificationConfig
}

func DefaultHAConfig() HAConfig {
	return HAConfig{
		Enabled: false,
		Monitor: MonitorConfig{
			Interval:            5 * time.Second,
			PingTimeout:         3 * time.Second,
			ConsecutiveFailures: 3,
		},
		Failover: FailoverConfig{
			Cooldown:       60 * time.Second,
			DryRun:         false,
			NotifyBefore:   true,
			ProxySwitchURL: "",
		},
		Notification: NotificationConfig{
			LogOnly: true,
		},
	}
}

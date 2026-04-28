package monitor

import "time"

type MonitorConfig struct {
	Interval            time.Duration
	PingTimeout         time.Duration
	ConsecutiveFailures int
}

type FailoverConfig struct {
	Cooldown      time.Duration
	DryRun        bool
	NotifyBefore  bool
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
			Cooldown:     60 * time.Second,
			DryRun:       false,
			NotifyBefore: true,
		},
		Notification: NotificationConfig{
			LogOnly: true,
		},
	}
}

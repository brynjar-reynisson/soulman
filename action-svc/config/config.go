package config

import (
	"fmt"
	"os"

	"soulman/common/sharedconfig"
)

type Config struct {
	NATSURL                string
	HTTPPort               string
	SoulmanRoot            string
	ReportSendTime         string
	ReportNotifier         string
	DiscordBotToken        string
	DiscordChannelID       string
	ThinkingRequestSubject string
	MemoryWriteSubject     string
	ActionSvcConsumerName  string
	FeignMode              bool
	DNDEnabled             bool
	DNDStart               string
	DNDEnd                 string

	SchoolEnabled                 bool
	SchoolNotifyTime              string
	SchoolCalendarRecipientEmails []string
	CalendarClientID              string
	CalendarClientSecret          string
	CalendarRefreshToken          string
}

func Load() (*Config, error) {
	configPath := env("CONFIG_PATH", "./config.json")

	shared, err := sharedconfig.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading shared config: %w", err)
	}
	if shared.NATSURL == "" {
		return nil, fmt.Errorf("shared config %s has no nats_url configured", configPath)
	}
	if shared.ThinkingRequestSubject == "" {
		return nil, fmt.Errorf("shared config %s has no thinking_request_subject configured", configPath)
	}
	if shared.MemoryWriteSubject == "" {
		return nil, fmt.Errorf("shared config %s has no memory_write_subject configured", configPath)
	}
	if shared.ConsumerNames.ActionSvc == "" {
		return nil, fmt.Errorf("shared config %s has no consumer_names.action_svc configured", configPath)
	}

	return &Config{
		NATSURL:                shared.NATSURL,
		HTTPPort:               env("HTTP_PORT", "9004"),
		SoulmanRoot:            env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
		ReportSendTime:         env("REPORT_SEND_TIME", "10:00"),
		ReportNotifier:         env("REPORT_NOTIFIER", "discord"),
		DiscordBotToken:        env("DISCORD_BOT_TOKEN", ""),
		DiscordChannelID:       env("DISCORD_CHANNEL_ID", ""),
		ThinkingRequestSubject: shared.ThinkingRequestSubject,
		MemoryWriteSubject:     shared.MemoryWriteSubject,
		ActionSvcConsumerName:  shared.ConsumerNames.ActionSvc,
		FeignMode:              shared.FeignMode,
		DNDEnabled:             shared.DoNotDisturb.Enabled,
		DNDStart:               shared.DoNotDisturb.Start,
		DNDEnd:                 shared.DoNotDisturb.End,

		SchoolEnabled:                 shared.School.Enabled,
		SchoolNotifyTime:              orDefault(shared.School.NotifyTime, "16:00"),
		SchoolCalendarRecipientEmails: shared.School.CalendarRecipientEmails,
		CalendarClientID:              env("CALENDAR_CLIENT_ID", ""),
		CalendarClientSecret:          env("CALENDAR_CLIENT_SECRET", ""),
		CalendarRefreshToken:          env("CALENDAR_REFRESH_TOKEN", ""),
	}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

package main

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
)

type SlackReporter struct {
	ChainInfoConfig ChainInfoConfig
	SlackConfig     SlackConfig
	Params          *Params
	Logger          zerolog.Logger

	SlackClient slack.Client
}

func NewSlackReporter(
	chainInfoConfig ChainInfoConfig,
	slackConfig SlackConfig,
	params *Params,
	logger *zerolog.Logger,
) *SlackReporter {
	return &SlackReporter{
		ChainInfoConfig: chainInfoConfig,
		SlackConfig:     slackConfig,
		Params:          params,
		Logger:          logger.With().Str("component", "slack_reporter").Logger(),
	}
}

func (r SlackReporter) Serialize(report Report) string {
	var sb strings.Builder

	for _, entry := range report.Entries {
		var (
			validatorLink string
			timeToJail    = ""
		)

		if entry.Direction == INCREASING {
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail(r.Params))
		}

		validatorLink = fmt.Sprintf(
			"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
			r.ChainInfoConfig.MintscanPrefix,
			entry.ValidatorAddress,
			entry.ValidatorMoniker,
		)

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>%s\n",
			entry.Emoji,
			validatorLink,
			entry.Description,
			timeToJail,
		))
	}

	return sb.String()
}

func (r *SlackReporter) Init() {
	if r.SlackConfig.Token == "" || r.SlackConfig.Chat == "" {
		r.Logger.Debug().Msg("Slack credentials not set, not creating Slack reporter.")
		return
	}

	client := slack.New(r.SlackConfig.Token)
	r.SlackClient = *client
}

func (r SlackReporter) Enabled() bool {
	return r.SlackConfig.Token != "" && r.SlackConfig.Chat != ""
}

func (r SlackReporter) SendReport(report Report) error {
	serializedReport := r.Serialize(report)
	_, _, err := r.SlackClient.PostMessage(
		r.SlackConfig.Chat,
		slack.MsgOptionText(serializedReport, false),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	return err
}

func (r SlackReporter) Name() string {
	return "SlackReporter"
}

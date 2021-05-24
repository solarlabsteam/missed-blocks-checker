package main

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

type SlackReporter struct {
	SlackToken string
	SlackChat  string

	SlackClient slack.Client
}

func (r SlackReporter) Serialize(report Report) string {
	var sb strings.Builder

	for _, entry := range report.Entries {
		var (
			emoji         string
			status        string
			validatorLink string
		)

		switch entry.Direction {
		case START_MISSING_BLOCKS:
			emoji = "🚨"
			status = "is missing blocks"
		case MISSING_BLOCKS:
			emoji = "🔴"
			status = "is missing blocks"
		case STOPPED_MISSING_BLOCKS:
			emoji = "🟡"
			status = "stopped missing blocks"
		case WENT_BACK_TO_NORMAL:
			emoji = "🟢"
			status = "went back to normal"
		}

		if entry.ValidatorAddress != "" {
			validatorLink = fmt.Sprintf(
				"<https://www.mintscan.io/%s/validators/%s|%s>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorMoniker,
			)
		} else if entry.ValidatorMoniker == "" { // validator with empty moniker, can happen
			validatorLink = fmt.Sprintf(
				"<https://www.mintscan.io/%s/validators/%s|validator with key %s>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.Pubkey,
			)
		} else {
			validatorLink = fmt.Sprintf("validator with key `%s`", entry.Pubkey)
		}

		sb.WriteString(fmt.Sprintf(
			"%s *%s %s*: %d -> %d\n",
			emoji,
			validatorLink,
			status,
			entry.BeforeBlocksMissing,
			entry.NowBlocksMissing,
		))
	}

	return sb.String()
}

func (r *SlackReporter) Init() {
	if r.SlackToken == "" || r.SlackChat == "" {
		log.Debug().Msg("Slack credentials not set, not creating Slack reporter.")
		return
	}

	client := slack.New(r.SlackToken)
	r.SlackClient = *client
}

func (r SlackReporter) Enabled() bool {
	return r.SlackToken != "" && r.SlackChat != ""
}

func (r SlackReporter) SendReport(report Report) error {
	serializedReport := r.Serialize(report)
	_, _, err := r.SlackClient.PostMessage(
		r.SlackChat,
		slack.MsgOptionText(serializedReport, false),
	)
	return err
}

func (r SlackReporter) Name() string {
	return "SlackReporter"
}

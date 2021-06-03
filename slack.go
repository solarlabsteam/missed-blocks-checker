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
			timeToJail    string = ""
		)

		switch entry.Direction {
		case START_MISSING_BLOCKS:
			emoji = "🚨"
			status = "is missing blocks"
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		case MISSING_BLOCKS:
			emoji = "🔴"
			status = "is missing blocks"
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		case STOPPED_MISSING_BLOCKS:
			emoji = "🟡"
			status = "stopped missing blocks"
		case WENT_BACK_TO_NORMAL:
			emoji = "🟢"
			status = "went back to normal"
		case JAILED:
			emoji = "❌"
			status = "was jailed"
		}

		if entry.ValidatorAddress != "" && entry.ValidatorMoniker != "" {
			validatorLink = fmt.Sprintf(
				"<https://www.mintscan.io/%s/validators/%s|%s>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorMoniker,
			)
		} else if entry.ValidatorMoniker == "" { // validator with empty moniker, can happen
			validatorLink = fmt.Sprintf(
				"<https://www.mintscan.io/%s/validators/%s|%s>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorAddress,
			)
		} else {
			validatorLink = fmt.Sprintf("`%s`", entry.Pubkey)
		}

		sb.WriteString(fmt.Sprintf(
			"%s *%s %s*: %d -> %d%s\n",
			emoji,
			validatorLink,
			status,
			entry.BeforeBlocksMissing,
			entry.NowBlocksMissing,
			timeToJail,
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

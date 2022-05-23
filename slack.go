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
			validatorLink string
			timeToJail    string = ""
		)

		switch entry.Direction {
		case INCREASING:
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		}

		validatorLink = fmt.Sprintf(
			"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
			Config.MintscanPrefix,
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
		slack.MsgOptionDisableLinkUnfurl(),
	)
	return err
}

func (r SlackReporter) Name() string {
	return "SlackReporter"
}

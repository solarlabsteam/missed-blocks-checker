package main

import (
	"fmt"
	"strings"
	"time"

	telegramBot "gopkg.in/tucnak/telebot.v2"
)

type TelegramReporter struct {
	TelegramToken string
	TelegramChat  int

	TelegramBot *telegramBot.Bot
}

func (r TelegramReporter) Serialize(report Report) string {
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
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorMoniker,
			)
		} else if entry.ValidatorMoniker == "" { // validator with empty moniker, can happen
			validatorLink = fmt.Sprintf(
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorAddress,
			)
		} else {
			validatorLink = fmt.Sprintf("<code>%s</code>", entry.Pubkey)
		}

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>: %d -> %d%s\n",
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

func (r *TelegramReporter) Init() {
	if r.TelegramToken == "" || r.TelegramChat == 0 {
		log.Debug().Msg("Telegram credentials not set, not creating Telegram reporter.")
		return
	}

	bot, err := telegramBot.NewBot(telegramBot.Settings{
		Token:  TelegramToken,
		Poller: &telegramBot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Warn().Err(err).Msg("Could not create Telegram bot")
		return
	}

	r.TelegramBot = bot
}

func (r TelegramReporter) Enabled() bool {
	return r.TelegramBot != nil
}

func (r TelegramReporter) SendReport(report Report) error {
	serializedReport := r.Serialize(report)
	_, err := r.TelegramBot.Send(
		&telegramBot.User{
			ID: r.TelegramChat,
		},
		serializedReport,
		telegramBot.ModeHTML,
	)
	return err
}

func (r TelegramReporter) Name() string {
	return "TelegramReporter"
}
